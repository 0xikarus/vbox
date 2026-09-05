package cli

import (
	"bytes"
	"context"
	"encoding/json"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProcessPositionalAgents(t *testing.T) {
	for _, agent := range []string{"codex", "claude", "shell"} {
		o, err := parseControllerTaskOptions([]string{"box", agent, "--prompt", "work", "--idempotency-key", "stable"})
		if err != nil || o.agent != agent || o.box != "box" || o.idempotency != "stable" {
			t.Fatal(o, err)
		}
	}
	for _, args := range [][]string{{"box", "codex", "--agent", "claude", "--prompt", "x"}, {"box", "--agent", "claude", "codex", "--prompt", "x"}} {
		if _, err := parseControllerTaskOptions(args); err == nil {
			t.Fatal("ambiguous agent accepted")
		}
	}
}

func TestInteractiveOverridesReachOpenDispatcher(t *testing.T) {
	for _, agent := range []string{"codex", "claude", "shell"} {
		a := New()
		a.Environ = map[string]string{"TEST_TOKEN": "test"}
		a.IsTerminal = func() bool { return false }
		err := a.controller(context.Background(), config.File{}, config.Context{TokenEnv: "TEST_TOKEN"}, []string{"helper1", agent})
		if err == nil || !strings.Contains(err.Error(), "requires an interactive terminal") {
			t.Fatalf("%s did not reach interactive open: %v", agent, err)
		}
	}
}

func TestInteractivePickerStartsChosenAgent(t *testing.T) {
	chosen := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/logical-boxes/id/sessions/primary":
			json.NewEncoder(w).Encode(map[string]string{"session": ""})
		case "/v1/logical-boxes/id/sessions":
			json.NewEncoder(w).Encode(v1.SessionInventory{State: "live", Sessions: []v1.Session{}})
		case "/v1/logical-boxes/id/sessions/interactive":
			var in struct {
				Agent string `json:"agent"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			chosen = in.Agent
			http.Error(w, `{"error":"stop after confirmed selection"}`, 409)
		default:
			t.Error(r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := New()
	a.In = strings.NewReader("\x1b[B\r")
	a.Out = &bytes.Buffer{}
	a.Err = &bytes.Buffer{}
	err := a.openInteractive(context.Background(), config.Context{Controller: server.URL}, "test", v1.LogicalBox{ID: "id", DefaultAgent: "claude"}, "", "", false)
	if err == nil || chosen != "shell" {
		t.Fatal(chosen, err)
	}
}
