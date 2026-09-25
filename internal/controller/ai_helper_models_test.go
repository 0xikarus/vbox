package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestAIHelperSettingsRevealOnlyOwnerScopedSavedKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	envelope, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := envelope.Seal(aiHelperKeyScope("account-a"), []byte("sk-or-v1-synthetic-secret"))
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT encrypted_key,model FROM ai_helper_settings").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "model"}).AddRow(sealed, "anthropic/test"))
	server := &Server{Store: &Store{DB: db, Envelope: envelope}}
	response := httptest.NewRecorder()
	server.getAIHelperOpenRouter(response, httptest.NewRequest(http.MethodGet, "/v1/ai/openrouter", nil), Principal{AccountID: "account-a", Role: "owner"})
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"key":"sk-or-v1-synthetic-secret"`) {
		t.Fatalf("saved key was not returned privately: %d %s", response.Code, response.Body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAIHelperOpenCodeImportAllowsDifferentModel(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	envelope, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	profile := v1.SaveLoginProfileRequest{Files: map[string][]byte{
		"auth.json":     []byte(`{"openrouter":{"type":"api","key":"sk-or-v1-imported-test-key"}}`),
		"opencode.json": []byte(`{"model":"openrouter/anthropic/saved-model"}`),
	}}
	plain, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := envelope.Seal(profileEncryptionScope("account-a", "opencode", "saved"), plain)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT encrypted_value FROM login_profiles").WithArgs("account-a", "opencode", "saved").WillReturnRows(sqlmock.NewRows([]string{"encrypted_value"}).AddRow(sealed))
	server := &Server{Store: &Store{DB: db, Envelope: envelope}}
	request := httptest.NewRequest(http.MethodGet, "/v1/ai/openrouter/profiles/saved", nil)
	request.SetPathValue("name", "saved")
	response := httptest.NewRecorder()
	server.getAIHelperOpenCodeProfile(response, request, Principal{AccountID: "account-a", Role: "owner"})
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"key":"sk-or-v1-imported-test-key"`) || !strings.Contains(response.Body.String(), `"model":"anthropic/saved-model"`) {
		t.Fatalf("profile import did not return key and suggested model: %d %s", response.Code, response.Body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAIHelperCatalogIncludesTextModelsWithoutTools(t *testing.T) {
	client := &http.Client{Transport: modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer synthetic-private-key" {
			t.Error("catalog did not use saved key")
		}
		body := `{"data":[{"id":"text-only","name":"Text Only","architecture":{"output_modalities":["text"]}},{"id":"image-only","architecture":{"output_modalities":["image"]}}]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	models, err := queryOpenCodeModelCatalogFiltered(context.Background(), client, "openrouter", "synthetic-private-key", "https://openrouter.ai/api/v1/models", false)
	if err != nil || len(models) != 1 || models[0].ID != "openrouter/text-only" {
		t.Fatalf("writing catalog = %+v, %v", models, err)
	}
}

func TestAIHelperPromptModelOverridesConfiguredModel(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	envelope, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := envelope.Seal(aiHelperKeyScope("account-a"), []byte("sk-or-v1-synthetic-secret"))
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT encrypted_key,model FROM ai_helper_settings").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "model"}).AddRow(sealed, "anthropic/default"))
	client := &http.Client{Transport: modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Model string `json:"model"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "google/selected" {
			t.Errorf("wrong per-prompt model: %q", body.Model)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"content":"Hello"}}]}`)), Header: make(http.Header)}, nil
	})}
	server := &Server{Store: &Store{DB: db, Envelope: envelope}, HTTP: client}
	response := httptest.NewRecorder()
	server.rewriteText(response, httptest.NewRequest(http.MethodPost, "/v1/ai/rewrite", bytes.NewBufferString(`{"kind":"chat","text":"Helo","model":"openrouter/google/selected"}`)), Principal{AccountID: "account-a", Role: "owner"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"text":"Hello"`) {
		t.Fatalf("rewrite failed: %d %s", response.Code, response.Body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
