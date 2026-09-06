package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestSavedControllerLoginAndLogout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-login" {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	a := New()
	a.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	a.Environ = map[string]string{}
	a.Out = io.Discard
	a.IsTerminal = func() bool { return true }
	a.authPrompt = func(string) (string, error) { return "test-login", nil }
	c := config.Context{Name: "test", Controller: server.URL, TokenEnv: "TOKEN"}
	if _, err := a.controllerToken(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(a.tokenPath(c))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("token permissions", err)
	}
	b := New()
	b.ConfigPath, b.Environ, b.Out = a.ConfigPath, a.Environ, io.Discard
	b.IsTerminal = func() bool { return false }
	if token, err := b.controllerToken(context.Background(), c); err != nil || token != "test-login" {
		t.Fatal("saved login not reused")
	}
	other := c
	other.Controller += "/other"
	if _, err := b.controllerToken(context.Background(), other); err == nil {
		t.Fatal("cross-controller token reuse")
	}
	if err := b.controllerLogout(c); err != nil {
		t.Fatal(err)
	}
	if _, err := b.controllerToken(context.Background(), c); err == nil {
		t.Fatal("logout retained token")
	}
	a.authPrompt = func(string) (string, error) { return "wrong", nil }
	if _, err := a.controllerToken(context.Background(), c); err == nil {
		t.Fatal("accepted invalid token")
	}
	if _, err := os.Stat(a.tokenPath(c)); !os.IsNotExist(err) {
		t.Fatal("invalid token saved")
	}
}

func TestRejectedEnvironmentTokenReusesLoginAcrossProcesses(t *testing.T) {
	accepted := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		accepted++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	c := config.Context{Name: "team", Controller: server.URL, TokenEnv: "TOKEN"}
	prompts := 0
	for i := 0; i < 3; i++ {
		a := New() // Separate CLI invocation; no in-memory auth replacements.
		a.ConfigPath, a.Err = path, io.Discard
		a.Environ = map[string]string{"TOKEN": "stale-shell-export"}
		a.IsTerminal = func() bool { return true }
		a.authPrompt = func(string) (string, error) { prompts++; return "fresh", nil }
		token, err := a.controllerToken(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.request(context.Background(), c, token, http.MethodGet, "/v1/whoami", nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if prompts != 1 {
			t.Fatalf("invocation %d prompted again despite saved login", i)
		}
	}
	if accepted != 3 {
		t.Fatalf("accepted requests: %d", accepted)
	}
}

func TestEnvironmentAuthFallbackDoesNotMaskServerErrorsOrAffectAutomation(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusBadGateway, http.StatusOK} {
		for _, interactive := range []bool{true, false} {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Authorization") == "Bearer saved" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.WriteHeader(status)
			}))
			a := New()
			a.ConfigPath = filepath.Join(t.TempDir(), "config.json")
			a.Environ = map[string]string{"TOKEN": "environment"}
			a.IsTerminal = func() bool { return interactive }
			a.authPrompt = func(string) (string, error) { t.Fatal("unexpected prompt"); return "", nil }
			c := config.Context{Name: "team", Controller: server.URL, TokenEnv: "TOKEN"}
			if err := a.saveControllerToken(c, "saved"); err != nil {
				t.Fatal(err)
			}
			got, _ := a.request(context.Background(), c, "environment", http.MethodGet, "/test", nil, nil, nil)
			wantRequests, wantStatus := 1, status
			if status == http.StatusUnauthorized && interactive {
				wantRequests, wantStatus = 2, http.StatusNoContent
			}
			if requests != wantRequests || got != wantStatus {
				t.Fatalf("interactive=%v status=%d: got %d requests/status %d", interactive, status, requests, got)
			}
			server.Close()
		}
	}
}
