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

func TestMissingControllerFailsClosedNonInteractively(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.File{Current: "local", Contexts: map[string]config.Context{
		"local": {Provider: "railway"},
	}}); err != nil {
		t.Fatal(err)
	}
	app := New()
	app.ConfigPath = path
	app.In, app.Out, app.Err = strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return false }

	err := app.Run(context.Background(), []string{"ls"})
	if err == nil || !strings.Contains(err.Error(), "provider-only context cannot be used") {
		t.Fatalf("error=%v", err)
	}
}

func TestFirstRunPromptsForAndSavesController(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer controller-token" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]v1.LogicalBox{})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "config.json")
	app := New()
	app.ConfigPath = path
	app.Environ = map[string]string{"VMBOX_CONTROLLER_TOKEN": "controller-token"}
	app.In = strings.NewReader(server.URL + "\n\n\n\n")
	var output, stderr bytes.Buffer
	app.Out, app.Err = &output, &stderr
	app.IsTerminal = func() bool { return true }

	if err := app.Run(context.Background(), []string{"ls"}); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := saved.Active("")
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Name != "production" || ctx.Controller != server.URL || ctx.Provider != "" || ctx.ProviderCredential != "" || ctx.TokenEnv != "VMBOX_CONTROLLER_TOKEN" {
		t.Fatalf("saved context=%+v", ctx)
	}
	for _, expected := range []string{"Controller URL", "Context name [production]", "never stored"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("prompt missing %q: %s", expected, stderr.String())
		}
	}
}

func TestStandaloneMustBeExplicitWhenContextHasNoController(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.File{Current: "local", Contexts: map[string]config.Context{
		"local": {Provider: "docker"},
	}}); err != nil {
		t.Fatal(err)
	}
	app := New()
	app.ConfigPath = path
	app.In, app.Out, app.Err = strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return false }
	err := app.Run(context.Background(), []string{"--standalone", "clean"})
	if err == nil || !strings.Contains(err.Error(), "standalone mode has been removed") {
		t.Fatalf("error=%v", err)
	}
}
