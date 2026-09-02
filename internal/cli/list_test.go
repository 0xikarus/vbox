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
	for _, expected := range []string{"NAME", "STATE", "OPEN / RESUME", "strategy", "running", "ams", "2 GiB", "vmbox strategy", "Ctrl-b", "vmbox ls --json"} {
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
		json.NewEncoder(w).Encode([]v1.Run{{ID: "run-1", State: v1.JobRunning, Request: v1.CreateRunRequest{Box: "worker", Provider: "railway", Region: "ams", Resources: provider.Resources{CPU: 2, MemoryMiB: 4096, DiskGiB: 20}}}})
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
	for _, expected := range []string{"BOX", "RUN ID", "worker", "run-1", "4 GiB", "vmbox worker"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("controller list missing %q: %s", expected, output.String())
		}
	}
}
