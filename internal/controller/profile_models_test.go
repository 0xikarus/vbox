package controller

import (
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

func TestOpenCodeCatalogFiltersToolCapableTextModels(t *testing.T) {
	for _, tc := range []struct {
		provider string
		body     string
		want     string
	}{
			{"openrouter", `{"data":[{"id":"eligible","name":"Eligible","context_length":1000000,"pricing":{"prompt":"0.000003","completion":"0.000015"},"architecture":{"output_modalities":["text"],"input_modalities":["text","image"]},"supported_parameters":["tools","reasoning"]},{"id":"no-tools","architecture":{"output_modalities":["text"]}},{"id":"image","architecture":{"output_modalities":["image"]},"supported_parameters":["tools"]}]}`, "openrouter/eligible"},
		{"venice", `{"data":[{"id":"eligible","name":"Eligible","type":"text","model_spec":{"capabilities":{"supportsFunctionCalling":true,"supportsReasoningEffort":true}}},{"id":"offline","type":"text","model_spec":{"offline":true,"capabilities":{"supportsFunctionCalling":true}}},{"id":"no-tools","type":"text"}]}`, "venice/eligible"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			client := &http.Client{Transport: modelRoundTrip(func(r *http.Request) (*http.Response, error) {
				if got := r.Header.Get("Authorization"); got != "Bearer synthetic-key" {
					t.Errorf("wrong provider Authorization header")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			models, err := queryOpenCodeModelCatalog(context.Background(), client, tc.provider, "synthetic-key", "https://provider.example.test/models")
			if err != nil {
				t.Fatal(err)
			}
			if len(models) != 1 || models[0].ID != tc.want || models[0].Label != "Eligible" || !models[0].Reasoning {
				t.Fatalf("wrong provider model choices: %+v", models)
			}
			if tc.provider == "openrouter" && (models[0].InputCost != 3 || models[0].OutputCost != 15 || models[0].Context != 1000000) {
				t.Fatalf("pricing or context not parsed: %+v", models[0])
			}
		})
	}
}

type modelRoundTrip func(*http.Request) (*http.Response, error)

func (f modelRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProfileModelEndpointLoadsSavedProviderKeyWithoutExposingIt(t *testing.T) {
	store, mock := testStore(t)
	var err error
	store.Envelope, err = secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	profile := v1.SaveLoginProfileRequest{Files: map[string][]byte{
		"auth.json":     []byte(`{"openrouter":{"type":"api","key":"synthetic-private-key"}}`),
		"opencode.json": []byte(`{"model":"openrouter/eligible"}`),
	}}
	plain, _ := json.Marshal(profile)
	sealed, err := store.Envelope.Seal(profileEncryptionScope("account", "opencode", "saved"), plain)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT encrypted_value FROM login_profiles").WithArgs("account", "opencode", "saved").WillReturnRows(sqlmock.NewRows([]string{"encrypted_value"}).AddRow(sealed))
	server := &Server{Store: store, HTTP: &http.Client{Transport: modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://openrouter.ai/api/v1/models" || r.Header.Get("Authorization") != "Bearer synthetic-private-key" {
			t.Error("provider request does not use the saved key or fixed URL")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"eligible","name":"Eligible","architecture":{"output_modalities":["text"]},"supported_parameters":["tools"]}]}`)), Header: make(http.Header)}, nil
	})}}
	request := httptest.NewRequest(http.MethodGet, "/v1/login-profiles/opencode/saved/models", nil)
	request.SetPathValue("application", "opencode")
	request.SetPathValue("name", "saved")
	response := httptest.NewRecorder()
	server.getLoginProfileModels(response, request, Principal{AccountID: "account"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"openrouter/eligible"`) || strings.Contains(response.Body.String(), "synthetic-private-key") {
		t.Fatalf("unexpected model response: status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
