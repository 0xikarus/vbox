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
)

func TestControllerTaskSchedulesAgentWithQuietSummary(t *testing.T) {
	var request v1.CreateBoxTaskRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/logical-boxes/research/process-tasks" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Fatal("missing idempotency key")
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(v1.BoxTask{ID: "task-1", BoxName: "research", Agent: request.Agent, Session: request.Session, State: "queued"})
	}))
	defer server.Close()

	app := New()
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return false }
	c := config.Context{Controller: server.URL}
	if err := app.controllerTask(context.Background(), c, "secret", []string{"research", "--agent", "claude", "--session", "review", "--prompt", "inspect the change"}); err != nil {
		t.Fatal(err)
	}
	if request.Agent != "claude" || request.Session != "review" || request.Prompt != "inspect the change" {
		t.Fatalf("request=%+v", request)
	}
	output := app.Out.(*bytes.Buffer).String()
	if output != "research · task-1 · queued\n" || app.Err.(*bytes.Buffer).Len() != 0 {
		t.Fatalf("output=%q", output)
	}
}

func TestControllerTaskInteractiveDialogChoosesBoxAndAgent(t *testing.T) {
	var request v1.CreateBoxTaskRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/logical-boxes":
			_ = json.NewEncoder(w).Encode([]v1.LogicalBox{{ID: "box-1", Name: "research", State: v1.LogicalBoxHibernated}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/logical-boxes/research/process-tasks":
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(v1.BoxTask{ID: "task-2", BoxName: "research", Agent: request.Agent, Session: request.Session, State: "queued"})
		default:
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	app := New()
	app.In = strings.NewReader("1\n2\nwhat's today's date?\n")
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return true }
	c := config.Context{Controller: server.URL}
	if err := app.controllerTask(context.Background(), c, "secret", nil); err != nil {
		t.Fatal(err)
	}
	if request.Agent != "claude" || request.Session != "" || request.Prompt != "what's today's date?" {
		t.Fatalf("request=%+v", request)
	}
	if !strings.Contains(app.Out.(*bytes.Buffer).String(), "Select a logical box") || !strings.Contains(app.Out.(*bytes.Buffer).String(), "Choose an agent") {
		t.Fatalf("dialog output=%q", app.Out.(*bytes.Buffer).String())
	}
}

func TestControllerTaskNonInteractiveRequiresBoxAndPrompt(t *testing.T) {
	app := New()
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return false }
	if err := app.controllerTask(context.Background(), config.Context{}, "secret", nil); err == nil || !strings.Contains(err.Error(), "requires a logical box") {
		t.Fatalf("missing box error=%v", err)
	}
	if err := app.controllerTask(context.Background(), config.Context{}, "secret", []string{"research"}); err == nil || !strings.Contains(err.Error(), "requires --prompt") {
		t.Fatalf("missing prompt error=%v", err)
	}
}

func TestControllerTaskStatusUsesTaskAPIAndChecksBox(t *testing.T) {
	requested := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v1.BoxTask{ID: "task-1", LogicalBoxID: "box-1", BoxName: "research", Agent: "codex", State: "active"})
	}))
	defer server.Close()
	app := New()
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	if err := app.controllerTaskStatus(context.Background(), config.Context{Controller: server.URL}, "secret", []string{"research", "task-1"}); err != nil {
		t.Fatal(err)
	}
	if requested != "GET /v1/process-tasks/task-1" {
		t.Fatalf("task status requested %q", requested)
	}
	if err := app.controllerTaskStatus(context.Background(), config.Context{Controller: server.URL}, "secret", []string{"other", "task-1"}); err == nil || !strings.Contains(err.Error(), "belongs to logical box") {
		t.Fatalf("box mismatch error=%v", err)
	}
}

func TestControllerTaskStatusListsBoxTasksWithoutID(t *testing.T) {
	requested := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]v1.BoxTask{{ID: "task-1", BoxName: "research", State: "active"}})
	}))
	defer server.Close()
	app := New()
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	if err := app.controllerTaskStatus(context.Background(), config.Context{Controller: server.URL}, "secret", []string{"research"}); err != nil {
		t.Fatal(err)
	}
	if requested != "GET /v1/logical-boxes/research/process-tasks" {
		t.Fatalf("task list requested %q", requested)
	}
}
