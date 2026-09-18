package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/procexec"
)

func TestProfileUploadDialogNeverCreatesBox(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		home := t.TempDir()
		path := filepath.Join(home, ".claude")
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, ".credentials.json"), []byte(`{"email":"person@example.test","synthetic":"profile"}`), 0600); err != nil {
			t.Fatal(err)
		}
		uploads := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == "GET" && r.URL.Path == "/v1/login-profiles":
				json.NewEncoder(w).Encode([]v1.LoginProfile{})
			case r.Method == "PUT" && r.URL.Path == "/v1/login-profiles/claude/person-example.test":
				uploads++
				json.NewEncoder(w).Encode(v1.LoginProfile{Application: "claude", Name: "person-example.test"})
			default:
				t.Error("unexpected operation", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		a := New()
		a.Environ = map[string]string{"HOME": home}
		a.Runner = &procexec.FakeRunner{}
		a.IsTerminal = func() bool { return true }
		var output, screen bytes.Buffer
		a.Out, a.Err = &output, &screen
		keys := " \r " // Space enables, Enter disables, Space enables again.
		if cancel {
			keys += "\x03"
		} else {
			keys += strings.Repeat("\t", 3) + "\r"
		}
		a.In = strings.NewReader(keys)
		err := a.controllerLoginProfiles(context.Background(), config.Context{Controller: server.URL}, "test", []string{"upload"})
		if cancel {
			if err == nil || uploads != 0 {
				t.Fatal("cancel uploaded")
			}
		} else if err != nil || uploads != 1 || !strings.Contains(output.String(), "No box created") {
			t.Fatal("upload failed", err, uploads)
		}
		if !strings.Contains(screen.String(), "[ Upload ]") {
			t.Fatal("wrong submit label")
		}
		for _, want := range []string{"Account", "Source", "[ ] claude", "[x] claude", "Space/Enter"} {
			if !strings.Contains(screen.String(), want) {
				t.Fatalf("table missing %q", want)
			}
		}
		server.Close()
	}
}

func TestProfileUploadDialogOffersOpenCodeAPIKeyEntry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/login-profiles" {
			t.Fatalf("unexpected operation %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode([]v1.LoginProfile{})
	}))
	defer server.Close()

	a := New()
	a.Environ = map[string]string{"HOME": t.TempDir()}
	a.Runner = &procexec.FakeRunner{}
	a.IsTerminal = func() bool { return true }
	var screen bytes.Buffer
	a.Out, a.Err = &bytes.Buffer{}, &screen
	a.In = strings.NewReader("\x03")

	if err := a.controllerLoginProfiles(context.Background(), config.Context{Controller: server.URL}, "test", []string{"upload"}); err == nil {
		t.Fatal("cancel accepted")
	}
	if !strings.Contains(screen.String(), "Add OpenCode API key") {
		t.Fatalf("OpenCode API-key upload option missing from dialog: %q", screen.String())
	}
}

func TestOpenCodeAPIKeyModelDiscoveryVerifiesAndFiltersToolModels(t *testing.T) {
	for _, tc := range []struct {
		id, label, verification, catalog, want string
	}{
		{
			id:           "openrouter",
			label:        "OpenRouter",
			verification: `{"data":{}}`,
			catalog:      `{"data":[{"id":"vendor/tool-model","architecture":{"output_modalities":["text"]},"supported_parameters":["tools"]},{"id":"vendor/plain-model","architecture":{"output_modalities":["text"]},"supported_parameters":["temperature"]}]}`,
			want:         "openrouter/vendor/tool-model",
		},
		{
			id:           "venice",
			label:        "Venice",
			verification: `{"data":{"accessPermitted":true}}`,
			catalog:      `{"data":[{"id":"tool-model","type":"text","model_spec":{"capabilities":{"supportsFunctionCalling":true}}},{"id":"plain-model","type":"text","model_spec":{"capabilities":{"supportsFunctionCalling":false}}}]}`,
			want:         "venice/tool-model",
		},
	} {
		t.Run(tc.id, func(t *testing.T) {
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic-private-key" {
					t.Fatal("API key was not sent as bearer authentication")
				}
				calls = append(calls, r.URL.Path)
				switch r.URL.Path {
				case "/verify":
					fmt.Fprint(w, tc.verification)
				case "/models":
					fmt.Fprint(w, tc.catalog)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			a := New()
			provider := openCodeAPIProvider{ID: tc.id, Label: tc.label, VerifyURL: server.URL + "/verify", ModelsURL: server.URL + "/models"}
			models, err := a.queryOpenCodeAPIModels(context.Background(), provider, "synthetic-private-key")
			if err != nil {
				t.Fatal(err)
			}
			if len(models) != 1 || models[0] != tc.want {
				t.Fatalf("models=%#v", models)
			}
			if strings.Join(calls, ",") != "/verify,/models" {
				t.Fatalf("provider calls=%#v", calls)
			}
		})
	}
}

func TestOpenCodeAPIKeyVerificationDoesNotExposeRejectedSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream diagnostic must not be repeated", http.StatusUnauthorized)
	}))
	defer server.Close()
	a := New()
	provider := openCodeAPIProvider{ID: "openrouter", Label: "OpenRouter", VerifyURL: server.URL, ModelsURL: server.URL}
	secret := "synthetic-private-key"
	_, err := a.queryOpenCodeAPIModels(context.Background(), provider, secret)
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "upstream diagnostic") {
		t.Fatalf("unsafe verification error: %v", err)
	}
}

func TestSaveOpenCodeAPIKeyProfileIncludesSelectedProviderAndModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v1/login-profiles/opencode/venice-work" {
			t.Fatalf("unexpected operation %s %s", r.Method, r.URL.Path)
		}
		var request v1.SaveLoginProfileRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		var auth map[string]struct {
			Type, Key string
		}
		if err := json.Unmarshal(request.Files["auth.json"], &auth); err != nil {
			t.Fatal(err)
		}
		if auth["venice"].Type != "api" || auth["venice"].Key != "synthetic-private-key" || len(auth) != 1 {
			t.Fatalf("auth profile shape=%#v", auth)
		}
		var configuration struct {
			Model    string `json:"model"`
			Provider map[string]struct {
				Models map[string]any `json:"models"`
			} `json:"provider"`
		}
		if err := json.Unmarshal(request.Files["opencode.json"], &configuration); err != nil {
			t.Fatal(err)
		}
		if configuration.Model != "venice/tool-model" || configuration.Provider["venice"].Models["tool-model"] == nil {
			t.Fatalf("OpenCode config=%#v", configuration)
		}
		json.NewEncoder(w).Encode(v1.LoginProfile{Application: "opencode", Name: "venice-work"})
	}))
	defer server.Close()

	a := New()
	provider := openCodeAPIProvider{ID: "venice", Label: "Venice"}
	profile, err := a.saveOpenCodeAPIKeyProfile(context.Background(), config.Context{Controller: server.URL}, "controller-token", "venice-work", provider, "synthetic-private-key", "venice/tool-model")
	if err != nil || profile.Application != "opencode" || profile.Name != "venice-work" {
		t.Fatal(profile, err)
	}
}

func TestProfileUploadDialogVerifiesSelectsAndSavesOpenCodeAPIKey(t *testing.T) {
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/login-profiles":
			json.NewEncoder(w).Encode([]v1.LoginProfile{})
		case r.Method == http.MethodGet && r.URL.Path == "/verify":
			if r.Header.Get("Authorization") != "Bearer synthetic-private-key" {
				t.Fatal("verification did not use the entered key")
			}
			fmt.Fprint(w, `{"data":{}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/models":
			fmt.Fprint(w, `{"data":[{"id":"vendor/tool-model","architecture":{"output_modalities":["text"]},"supported_parameters":["tools"]}]}`)
		case r.Method == http.MethodPut && r.URL.Path == "/v1/login-profiles/opencode/work":
			puts++
			var request v1.SaveLoginProfileRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(request.Files["auth.json"], []byte("synthetic-private-key")) || !bytes.Contains(request.Files["opencode.json"], []byte("openrouter/vendor/tool-model")) {
				t.Fatal("saved profile did not include the verified key and selected model")
			}
			json.NewEncoder(w).Encode(v1.LoginProfile{Application: "opencode", Name: "work"})
		default:
			t.Fatalf("unexpected operation %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	a := New()
	a.Environ = map[string]string{"HOME": t.TempDir()}
	a.Runner = &procexec.FakeRunner{}
	a.IsTerminal = func() bool { return true }
	a.openCodeAPIProviders = []openCodeAPIProvider{{ID: "openrouter", Label: "OpenRouter", VerifyURL: server.URL + "/verify", ModelsURL: server.URL + "/models"}}
	var output, screen bytes.Buffer
	a.Out, a.Err = &output, &screen
	// Open the API-key entry, set its name and hidden key, submit once to load
	// models, then submit the default (and only) tool-capable model.
	a.In = strings.NewReader("\x1b[B\r\t\rwork\r\t\rsynthetic-private-key\r\t\t\t\r\t\r")
	if err := a.controllerLoginProfiles(context.Background(), config.Context{Controller: server.URL}, "controller-token", []string{"upload"}); err != nil {
		t.Fatal(err)
	}
	if puts != 1 || !strings.Contains(output.String(), "Uploaded 1 login profile") {
		t.Fatal("OpenCode profile was not saved exactly once", puts, output.String())
	}
	if strings.Contains(screen.String(), "synthetic-private-key") {
		t.Fatal("API key was rendered in the terminal")
	}
}
