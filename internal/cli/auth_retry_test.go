package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestAuthenticationRetry(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		for _, accepted := range []bool{false, true} {
			requests, prompts := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if accepted && r.Header.Get("Authorization") == "Bearer fresh" {
					w.WriteHeader(http.StatusNoContent)
				} else {
					w.WriteHeader(http.StatusUnauthorized)
				}
			}))
			a := New()
			a.ConfigPath = filepath.Join(t.TempDir(), "config.json")
			a.Err = io.Discard
			a.IsTerminal = func() bool { return interactive }
			a.authPrompt = func(string) (string, error) { prompts++; return "fresh", nil }
			c := config.Context{Controller: server.URL, TokenEnv: "VMBOX_CONTROLLER_TOKEN"}
			_, err := a.request(context.Background(), c, "old", http.MethodGet, "/test", nil, nil, nil)
			if (err == nil) != (interactive && accepted) {
				t.Fatalf("interactive=%v accepted=%v err=%v", interactive, accepted, err)
			}
			if interactive {
				if prompts != 1 || requests != 2 {
					t.Fatalf("prompts=%d requests=%d", prompts, requests)
				}
				_, _ = a.request(context.Background(), c, "old", http.MethodGet, "/test", nil, nil, nil)
				if prompts != 1 || requests != 3 {
					t.Fatal("replacement not reused or repeated prompt")
				}
			} else if prompts != 0 || requests != 1 {
				t.Fatal("noninteractive retry")
			}
			server.Close()
		}
	}
}
