package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestControllerListIsReadable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/logical-boxes" {
			t.Errorf("unexpected provider discovery: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode([]v1.LogicalBox{{ID: "box-1", Name: "worker", State: v1.LogicalBoxRunning, SlotID: "slot-1"}})
	}))
	defer server.Close()
	app := New()
	app.Environ = map[string]string{"TOKEN": "secret"}
	var output bytes.Buffer
	app.Out, app.Err = &output, &bytes.Buffer{}
	c := config.Context{Name: "team", Provider: "railway", Controller: server.URL, TokenEnv: "TOKEN"}
	if err := app.controller(context.Background(), config.File{}, c, []string{"ls"}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"NAME", "STATE", "worker", "slot-1"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("controller list missing %q: %s", expected, output.String())
		}
	}
}

func TestControllerStatusUsesLogicalBoxName(t *testing.T) {
	requested := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v1.LogicalBox{ID: "box-1", Name: "worker", State: v1.LogicalBoxHibernated})
	}))
	defer server.Close()
	app := New()
	app.Environ = map[string]string{"TOKEN": "secret"}
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	c := config.Context{Name: "team", Provider: "railway", Controller: server.URL, TokenEnv: "TOKEN"}
	if err := app.controller(context.Background(), config.File{}, c, []string{"status", "worker"}); err != nil {
		t.Fatal(err)
	}
	if requested != "GET /v1/logical-boxes/worker/status" {
		t.Fatalf("status requested %q", requested)
	}
}

func TestControllerBareNameOpensLogicalBox(t *testing.T) {
	requested := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		requested = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v1.LogicalBox{ID: "box-1", Name: "worker", Provider: "railway", State: v1.LogicalBoxRunning})
	}))
	defer server.Close()

	configPath := t.TempDir() + "/config.json"
	if err := config.Save(configPath, config.File{
		Current: "team",
		Contexts: map[string]config.Context{
			"team": {Name: "team", Provider: "railway", Controller: server.URL, TokenEnv: "TOKEN"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	app := New()
	app.ConfigPath = configPath
	app.Environ = map[string]string{"TOKEN": "secret"}
	app.In, app.Out, app.Err = strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return false }

	err := app.Run(context.Background(), []string{"worker"})
	if err == nil || !strings.Contains(err.Error(), "opening a logical box requires an interactive terminal") {
		t.Fatalf("bare-name open error = %v", err)
	}
	if requested != "" {
		t.Fatalf("noninteractive attach contacted controller: %q", requested)
	}
}

func TestControllerResumeSelectsLogicalBoxesInsteadOfRuns(t *testing.T) {
	requested := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]v1.LogicalBox{{ID: "box-1", Name: "research", State: v1.LogicalBoxHibernated}})
	}))
	defer server.Close()
	app := New()
	app.Environ = map[string]string{"TOKEN": "secret"}
	app.In, app.Out, app.Err = strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return false }
	c := config.Context{Name: "team", Provider: "railway", Controller: server.URL, TokenEnv: "TOKEN"}
	err := app.controller(context.Background(), config.File{}, c, []string{"resume"})
	if err == nil || !strings.Contains(err.Error(), "selection requires an interactive terminal") {
		t.Fatalf("resume error=%v", err)
	}
	if requested != "GET /v1/logical-boxes" {
		t.Fatalf("resume requested %q", requested)
	}
	if !strings.Contains(app.Out.(*bytes.Buffer).String(), "research\thibernated") {
		t.Fatalf("resume list=%q", app.Out.(*bytes.Buffer).String())
	}
}

func TestControllerWelcomeUploadUsesPrivateHomeFileMode(t *testing.T) {
	app := New()
	app.Err = &bytes.Buffer{}
	var argv []string
	execute := func(_ context.Context, got []string, _ provider.ExecOptions) (provider.ExecResult, error) {
		argv = append([]string(nil), got...)
		return provider.ExecResult{}, nil
	}
	resolved := v1.LogicalBoxConnection{BoxName: "research", Connection: provider.Connection{Metadata: map[string]string{"vmboxBoxName": "research"}}}
	if err := app.refreshControllerWelcome(context.Background(), resolved, execute); err != nil {
		t.Fatal(err)
	}
	want := []string{"vmbox-runtime", "put-file", "/data/home/.vmbox-welcome", "0600"}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("welcome argv=%q", argv)
	}
}

func TestInventoryListDistinguishesUnobservedAndStaleState(t *testing.T) {
	var output bytes.Buffer
	if err := writeInventoryList(&output, v1.BoxInventory{Infrastructure: &provider.InventoryObservation{Stale: true}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "not been observed") || strings.Contains(output.String(), "No controller-visible boxes") {
		t.Fatalf("unobserved inventory reported empty: %s", output.String())
	}
	output.Reset()
	inventory := v1.BoxInventory{
		Infrastructure: &provider.InventoryObservation{Available: true, Stale: true, RefreshFailed: true},
		LogicalBoxes:   []v1.LogicalBox{{Name: "still-running", State: v1.LogicalBoxRunning}},
	}
	if err := writeInventoryList(&output, inventory); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "last refresh failed") || !strings.Contains(output.String(), "still-running") {
		t.Fatalf("failed provider refresh hid logical boxes: %s", output.String())
	}
}
