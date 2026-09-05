package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestProviderGuidedFields(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		wantError   bool
	}{
		{"valid", "project\n\ntrue\n", false},
		{"missing required", "\n", true},
		{"invalid boolean", "project\n\nperhaps\n", true},
		{"truncated", "project\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New()
			a.Err = &bytes.Buffer{}
			got, err := a.promptProviderFields(bufio.NewReader(strings.NewReader(tc.input)), map[string]string{"project": "string", "image": "string", "tls": "boolean"}, []string{"project"})
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if err == nil && (got["project"] != "project" || got["tls"] != true || len(got) != 2) {
				t.Fatalf("wrong config: %v", got)
			}
		})
	}
}

func TestProviderReadableListAndDetails(t *testing.T) {
	value := map[string]any{"provider": "test-provider", "name": "primary", "accountId": "account-private-metadata", "config": map[string]any{"location": "nearby", "tls": true}}
	for _, tc := range []struct {
		name               string
		value              any
		json, verbose      bool
		contains, excludes string
	}{
		{"list", []any{value}, false, false, "test-provider · primary", "location"},
		{"show", value, false, false, "location: nearby", "account-private-metadata"},
		{"verbose", value, false, true, "accountId: account-private-metadata", "\"config\""},
		{"json", value, true, false, "\"config\"", " · "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New()
			var out bytes.Buffer
			a.Out = &out
			a.Verbose = tc.verbose
			if err := a.providerOutput(tc.value, tc.json); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.contains) || strings.Contains(out.String(), tc.excludes) {
				t.Fatalf("unexpected output: %s", out.String())
			}
		})
	}
}

func TestProviderSetupConfirmationAndSecretRedaction(t *testing.T) {
	for _, confirm := range []bool{false, true} {
		writes := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/provider-schemas" {
				json.NewEncoder(w).Encode(map[string]any{"providers": map[string]any{"test": map[string]string{"project": "string"}}, "required": map[string][]string{"test": {"project"}}})
				return
			}
			if r.Method != "PUT" || r.URL.Path != "/v1/provider-credentials/test/primary" {
				t.Error("unexpected request")
				return
			}
			var body struct {
				Config map[string]string
				Secret map[string]string
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Config["project"] != "example" || body.Secret["token"] != "synthetic-private-value" {
				t.Error("incorrect payload")
			}
			writes++
			json.NewEncoder(w).Encode(v1.ProviderCredential{Provider: "test", Name: "primary"})
		}))
		a := New()
		var output bytes.Buffer
		a.Out, a.Err = &output, &output
		keys := "\nexample\n\n"
		if confirm {
			keys += "\x1b[A"
		}
		a.In = strings.NewReader(keys + "\r")
		a.IsTerminal = func() bool { return true }
		a.Environ = map[string]string{"VMBOX_PROVIDER_SECRET": `{"token":"synthetic-private-value"}`}
		_, err := a.setupProvider(context.Background(), config.Context{Controller: server.URL}, "test", "test")
		server.Close()
		if (err == nil) != confirm || (writes == 1) != confirm {
			t.Fatalf("confirm=%v writes=%d error=%v", confirm, writes, err)
		}
		if strings.Contains(output.String(), "synthetic-private-value") {
			t.Fatal("secret leaked")
		}
	}
}

func TestProviderPickerConfirmsBeforeSaving(t *testing.T) {
	for _, tc := range []struct {
		name, keys      string
		terminal, saved bool
	}{
		{"confirmed", "\x1b[B\r\x1b[A\r", true, true},
		{"cancelled", "\r\r", true, false},
		{"script", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && r.URL.Path == "/v1/provider-credentials" {
					json.NewEncoder(w).Encode([]v1.ProviderCredential{{Provider: "docker", Name: "local"}, {Provider: "railway", Name: "primary"}})
					return
				}
				if r.Method != "PUT" || r.URL.Path != "/v1/controller-defaults" {
					t.Error("unexpected request", r.Method, r.URL.Path)
					return
				}
				var value v1.FleetConfig
				json.NewDecoder(r.Body).Decode(&value)
				if value.Provider != "railway" || value.ProviderCredential != "primary" {
					t.Error("wrong provider selected")
				}
				writes++
				json.NewEncoder(w).Encode(value)
			}))
			defer server.Close()
			a := New()
			a.In, a.Out, a.Err = strings.NewReader(tc.keys), &bytes.Buffer{}, &bytes.Buffer{}
			a.IsTerminal = func() bool { return tc.terminal }
			_, err := a.chooseDefaultProvider(context.Background(), config.Context{Controller: server.URL}, "test")
			if (err == nil) != tc.saved || (writes == 1) != tc.saved {
				t.Fatalf("saved=%v writes=%d error=%v", tc.saved, writes, err)
			}
		})
	}
}

func TestProvidersReadableAndExplicitJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]v1.ProviderCredential{{Provider: "railway", Name: "primary"}})
	}))
	defer server.Close()
	for _, asJSON := range []bool{false, true} {
		a := New()
		var output bytes.Buffer
		a.Out = &output
		args := []string{}
		if asJSON {
			args = []string{"--json"}
		}
		if err := a.controllerProviders(context.Background(), config.Context{Controller: server.URL}, "test", args); err != nil {
			t.Fatal(err)
		}
		if asJSON {
			if !json.Valid(output.Bytes()) {
				t.Fatal("invalid JSON")
			}
		} else if output.String() != "railway · primary\n" {
			t.Fatal(output.String())
		}
	}
}
