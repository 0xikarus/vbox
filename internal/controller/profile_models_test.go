package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestCodexCatalogUsesSavedProfileAndAdvertisedReasoningLevels(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "codex-fixture")
	script := `#!/bin/sh
read initialize
read initialized
read model_list
case "$initialize$model_list" in
  *vmbox-model-catalog*model/list*) ;;
  *) exit 2 ;;
esac
test -f "$CODEX_HOME/auth.json" || exit 3
printf '%s\n' '{"id":1,"result":{}}'
printf '%s\n' '{"id":2,"result":{"data":[{"id":"account-model","model":"account-model","displayName":"Account Model","hidden":false,"supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"ultra"}]},{"id":"hidden","model":"hidden","displayName":"Hidden","hidden":true,"supportedReasoningEfforts":[]}]}}'
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	models, err := queryCodexModelCatalog(context.Background(), map[string][]byte{
		"auth.json":   []byte(`{"tokens":{"access_token":"synthetic-private-token"}}`),
		"config.toml": []byte(`model = "saved-model"`),
	}, executable)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "account-model" || models[0].Label != "Account Model" || strings.Join(models[0].ReasoningEfforts, ",") != "low,ultra" {
		t.Fatalf("wrong Codex model choices: %+v", models)
	}
}

func TestClaudeCatalogUsesSavedOAuthAndFiltersUnsupportedEffort(t *testing.T) {
	client := &http.Client{Transport: modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.anthropic.com/v1/models?limit=1000" || r.Header.Get("Authorization") != "Bearer synthetic-private-token" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("Claude catalog request does not use the saved token and fixed API version")
		}
		body := `{"data":[{"id":"claude-opus-test","display_name":"Claude Opus Test","max_input_tokens":1000000,"capabilities":{"effort":{"supported":true,"low":{"supported":true},"high":{"supported":true},"max":{"supported":true}}}},{"id":"claude-haiku-test","display_name":"Claude Haiku Test","max_input_tokens":200000}],"has_more":false}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	models, err := queryClaudeModelCatalog(context.Background(), client, map[string][]byte{
		".credentials.json": []byte(`{"claudeAiOauth":{"accessToken":"synthetic-private-token"}}`),
	}, "https://api.anthropic.com/v1/models?limit=1000")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "claude-opus-test" || models[0].Context != 1000000 || strings.Join(models[0].ReasoningEfforts, ",") != "low,high" || models[1].Reasoning {
		t.Fatalf("wrong Claude model choices: %+v", models)
	}
}

func TestClaudeCatalogFailureDoesNotExposeCredential(t *testing.T) {
	client := &http.Client{Transport: modelRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{"error":"synthetic-private-token"}`)), Header: make(http.Header)}, nil
	})}
	_, err := queryClaudeModelCatalog(context.Background(), client, map[string][]byte{
		".credentials.json": []byte(`{"claudeAiOauth":{"accessToken":"synthetic-private-token"}}`),
	}, "https://api.anthropic.com/v1/models?limit=1000")
	if !errors.Is(err, errClaudeCatalogLogin) || strings.Contains(err.Error(), "synthetic-private-token") {
		t.Fatalf("credential leaked in catalog error: %v", err)
	}
}

func TestClaudeProfileModelEndpointReturnsCatalogWithoutToken(t *testing.T) {
	store, mock := testStore(t)
	var err error
	store.Envelope, err = secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	profile := v1.SaveLoginProfileRequest{Files: map[string][]byte{
		".credentials.json": []byte(`{"claudeAiOauth":{"accessToken":"synthetic-private-token"}}`),
	}}
	plain, _ := json.Marshal(profile)
	sealed, err := store.Envelope.Seal(profileEncryptionScope("account", "claude", "saved"), plain)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT encrypted_value FROM login_profiles").WithArgs("account", "claude", "saved").WillReturnRows(sqlmock.NewRows([]string{"encrypted_value"}).AddRow(sealed))
	server := &Server{Store: store, HTTP: &http.Client{Transport: modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.anthropic.com/v1/models?limit=1000" || r.Header.Get("Authorization") != "Bearer synthetic-private-token" {
			t.Error("Claude endpoint did not use its saved profile")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"claude-opus-test","display_name":"Claude Opus Test"}]}`)), Header: make(http.Header)}, nil
	})}}
	request := httptest.NewRequest(http.MethodGet, "/v1/login-profiles/claude/saved/models", nil)
	request.SetPathValue("application", "claude")
	request.SetPathValue("name", "saved")
	response := httptest.NewRecorder()
	server.getLoginProfileModels(response, request, Principal{AccountID: "account"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"claude-opus-test"`) || strings.Contains(response.Body.String(), "synthetic-private-token") {
		t.Fatalf("unexpected model response: status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

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
