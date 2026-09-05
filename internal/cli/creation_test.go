package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestCreationDialogDefersUploadsAndRetainsThemOnRetry(t *testing.T) {
	for _, cancel := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancel", false: "submit and retry"}[cancel], func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, ".claude")
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, ".credentials.json"), []byte(`{"synthetic":"profile"}`), 0600); err != nil {
				t.Fatal(err)
			}
			uploads, creates := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/provider-credentials":
					json.NewEncoder(w).Encode([]v1.ProviderCredential{{Provider: "test", Name: "primary"}})
				case "/v1/controller-defaults":
					json.NewEncoder(w).Encode(v1.FleetConfig{Provider: "test", ProviderCredential: "primary"})
				case "/v1/locations":
					json.NewEncoder(w).Encode([]v1.LocationPreset{{ID: "near"}})
				case "/v1/login-profiles":
					json.NewEncoder(w).Encode([]v1.LoginProfile{})
				case "/v1/login-profiles/claude/personal":
					uploads++
					json.NewEncoder(w).Encode(v1.LoginProfile{Application: "claude", Name: "personal"})
				case "/v1/logical-boxes":
					creates++
					var req v1.CreateLogicalBoxRequest
					json.NewDecoder(r.Body).Decode(&req)
					if req.DefaultAgent != "shell" || len(req.LoginProfiles) != 1 || req.LoginProfiles[0].Application != "claude" {
						t.Error("wrong selected profile or default")
					}
					if creates == 1 {
						http.Error(w, `{"error":"temporary capacity conflict"}`, 409)
						return
					}
					json.NewEncoder(w).Encode(v1.LogicalBox{ID: "box", Name: "test", State: v1.LogicalBoxHibernated})
				default:
					t.Error("unexpected route", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			a := New()
			var screen bytes.Buffer
			a.Err = &screen
			a.Out = &bytes.Buffer{}
			a.Environ = map[string]string{"HOME": home}
			a.IsTerminal = func() bool { return true }
			keys := strings.Repeat("\t", 4) + "\x1b[C" // select Upload local, no write yet
			if cancel {
				keys += "\x03"
			} else {
				keys += strings.Repeat("\t", 6) + "\r\r"
			}
			a.In = strings.NewReader(keys)
			err := a.createWorkspace(context.Background(), config.Context{Controller: server.URL}, "test", v1.CreateLogicalBoxRequest{Name: "test", DiskGiB: 10, DefaultAgent: "shell", AllocationRequestKey: "unique"}, creationHibernated, "", true, false, false)
			if cancel {
				if err == nil || uploads != 0 || creates != 0 {
					t.Fatal("cancel caused mutation", err, uploads, creates)
				}
			} else {
				if err != nil || uploads != 1 || creates != 2 {
					t.Fatal("retry lost upload state", err, uploads, creates)
				}
			}
			if strings.Count(screen.String(), "\x1b[?1049h") != 1 || strings.Count(screen.String(), "\x1b[?1049l") != 1 {
				t.Fatal("multiple dialogs")
			}
		})
	}
}

func TestCreationDefaultsToAllocateAndEnsureShell(t *testing.T) {
	var steps []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/logical-boxes":
			steps = append(steps, "create")
			json.NewEncoder(w).Encode(v1.LogicalBox{ID: "box", Name: "test", State: v1.LogicalBoxHibernated})
		case "/v1/logical-boxes/box/allocate":
			steps = append(steps, "allocate")
			json.NewEncoder(w).Encode(v1.Allocation{RequestID: "allocation", State: "ready"})
		case "/v1/logical-boxes/box":
			json.NewEncoder(w).Encode(v1.LogicalBox{ID: "box", Name: "test", State: v1.LogicalBoxRunning})
		case "/v1/logical-boxes/box/sessions/interactive":
			steps = append(steps, "shell")
			var req struct {
				Agent      string
				ReuseShell bool
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.Agent != "shell" || !req.ReuseShell {
				t.Error("creation did not use plain shell connection")
			}
			http.Error(w, `{"error":"stop before SSH"}`, 409)
		default:
			t.Error(r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := New()
	a.Out = &bytes.Buffer{}
	a.Err = &bytes.Buffer{}
	a.IsTerminal = func() bool { return true }
	err := a.createWorkspace(context.Background(), config.Context{Controller: server.URL}, "test", v1.CreateLogicalBoxRequest{Name: "test", DiskGiB: 10, DefaultAgent: "shell", AllocationRequestKey: "unique"}, creationConnect, "", false, true, false)
	if err == nil || strings.Join(steps, ",") != "create,allocate,shell" {
		t.Fatal(steps, err)
	}
}
