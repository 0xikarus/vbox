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

func TestStandaloneListIsReadableWithExplicitJSONFallback(t *testing.T) {
	p := newCLIProvider()
	p.boxes["strategy"] = provider.Box{Name: "strategy", State: provider.StateRunning, Region: "ams", Resources: provider.Resources{CPU: 1, MemoryMiB: 2048, DiskGiB: 10}}
	app := New()
	var output bytes.Buffer
	app.Out, app.Err = &output, &bytes.Buffer{}
	file := config.File{Contexts: map[string]config.Context{"test": {Name: "test", Provider: "test"}}}
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"ls"}); err != nil {
		t.Fatal(err)
	}
	readable := output.String()
	for _, expected := range []string{"NAME", "STATE", "MANAGEMENT", "OPEN / RESUME", "standalone", "strategy", "running", "ams", "2 GiB", "vmbox strategy", "Ctrl-a", "vmbox ls --json"} {
		if !strings.Contains(readable, expected) {
			t.Fatalf("readable list missing %q: %s", expected, readable)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(readable), "[") {
		t.Fatalf("default list is raw JSON: %s", readable)
	}

	output.Reset()
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"ls", "--json"}); err != nil {
		t.Fatal(err)
	}
	var boxes []provider.Box
	if err := json.Unmarshal(output.Bytes(), &boxes); err != nil || len(boxes) != 1 || boxes[0].Name != "strategy" {
		t.Fatalf("JSON boxes=%+v error=%v output=%s", boxes, err, output.String())
	}
}

func TestControllerListIsReadable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v1.BoxInventory{
			LogicalBoxes:   []v1.LogicalBox{{ID: "box-1", Name: "worker", Provider: "railway", State: v1.LogicalBoxRunning, VolumeName: "worker-data", SlotID: "slot-1"}},
			ConnectedBoxes: []v1.ConnectedBox{{ID: "outside-1", Name: "manual-service", Provider: "railway", State: provider.StateRunning, Management: "external"}},
		})
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
	for _, expected := range []string{"NAME", "MANAGEMENT", "worker", "worker-data", "controller", "vmbox worker", "manual-service", "external", "--standalone"} {
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
	if requested != "GET /v1/logical-boxes/worker" {
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
	if requested != "GET /v1/logical-boxes/worker" {
		t.Fatalf("bare name requested %q", requested)
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
