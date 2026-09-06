package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestBareCommandShowsInfoWithoutDialogOrMutation(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/whoami" {
				w.WriteHeader(204)
				return
			}
			requests++
			if r.Method != "GET" || r.URL.Path != "/v1/logical-boxes" {
				t.Error("unexpected operation", r.Method, r.URL.Path)
			}
			json.NewEncoder(w).Encode([]v1.LogicalBox{{Name: "test", State: v1.LogicalBoxDeleting, RestorationState: "detaching-volume"}})
		}))
		path := filepath.Join(t.TempDir(), "config.json")
		c := config.Context{Name: "test-context", Controller: server.URL, TokenEnv: "TEST_TOKEN"}
		if err := config.Save(path, config.File{Current: c.Name, Contexts: map[string]config.Context{c.Name: c}}); err != nil {
			t.Fatal(err)
		}
		for _, authenticated := range []bool{false, true} {
			requests = 0
			prompts := 0
			var out, stderr bytes.Buffer
			a := New()
			a.ConfigPath, a.Out, a.Err, a.In = path, &out, &stderr, strings.NewReader("")
			a.IsTerminal = func() bool { return terminal }
			a.Environ = map[string]string{}
			a.authPrompt = func(string) (string, error) { prompts++; return "synthetic", nil }
			if authenticated {
				a.Environ["TEST_TOKEN"] = "synthetic"
			}
			if err := a.Run(context.Background(), nil); err != nil {
				if !terminal && !authenticated && strings.Contains(err.Error(), "authentication missing") {
					continue
				}
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "\x1b[") || stderr.Len() != 0 || !strings.Contains(out.String(), "test-context") {
				t.Fatal("overview prompted or hid context", out.String(), stderr.String())
			}
			if prompts != 0 && (!terminal || authenticated) {
				t.Fatal("unexpected authentication prompt")
			}
			if authenticated || terminal {
				if terminal && !authenticated && prompts != 1 {
					t.Fatal("missing token prompt")
				}
				if requests != 1 || !strings.Contains(out.String(), "deleting") || !strings.Contains(out.String(), "detaching-volume") {
					t.Fatal("missing read-only state overview")
				}
			} else if requests != 0 || !strings.Contains(out.String(), "Authentication not configured") {
				t.Fatal("unauthenticated overview attempted connection or prompt")
			}
		}
		server.Close()
	}
}
