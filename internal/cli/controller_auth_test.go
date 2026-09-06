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
