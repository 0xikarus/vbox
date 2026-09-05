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
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestNativeCLIExactSessionAndNoReplay(t *testing.T) {
	fence := strings.Repeat("a", 64)
	inc := fence + ":" + strings.Repeat("b", 24) + ":$4"
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method != "GET" {
			t.Error("explicit attachment mutated controller")
		}
		var out any
		switch r.URL.Path {
		case "/v1/logical-boxes/id/sessions":
			out = v1.SessionInventory{LogicalBoxID: "id", State: "live", Assignment: fence, Sessions: []v1.Session{{ID: "$3", Name: "test-prefix", Incarnation: "other"}, {ID: "$4", Name: "test", Incarnation: inc}}}
		case "/v1/logical-boxes/id/native-connection":
			if r.URL.Query().Get("sessionId") != "$4" || r.URL.Query().Get("incarnation") != inc {
				t.Error("wrong session handoff")
			}
			out = v1.NativeConnection{LogicalBoxConnection: v1.LogicalBoxConnection{LogicalBoxID: "id", Session: "test", Connection: provider.Connection{Transport: "openssh", Endpoint: "user@host"}}, SessionID: "$4", Assignment: fence, Incarnation: inc}
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer server.Close()
	runner := &procexec.FakeRunner{Results: []procexec.Result{{ExitCode: 255}}}
	a := New()
	a.In = strings.NewReader("")
	a.Out = &bytes.Buffer{}
	a.Err = &bytes.Buffer{}
	a.Runner = runner
	a.Environ = map[string]string{}
	a.IsTerminal = func() bool { return true }
	err := a.attachNative(context.Background(), config.Context{Controller: server.URL}, "test-token", v1.LogicalBox{ID: "id", Name: "box"}, "test")
	if err == nil || !strings.Contains(err.Error(), "not replayed") {
		t.Fatalf("disconnect error=%v", err)
	}
	if len(runner.Calls) != 1 || runner.Calls[0].Argv[0] != "ssh" {
		t.Fatal("non-native dispatch or retry")
	}
	remote := strings.Join(runner.Calls[0].Argv, " ")
	if !strings.Contains(remote, "native-attach") || !strings.Contains(remote, "'$4'") || strings.Contains(remote, "new-session") {
		t.Fatal("attachment not exact/attach-only")
	}
	if len(requests) != 2 {
		t.Fatal("unexpected requests")
	}
	before := len(runner.Calls)
	if err = a.attachNative(context.Background(), config.Context{Controller: server.URL}, "test-token", v1.LogicalBox{ID: "id"}, "tes"); err == nil {
		t.Fatal("prefix accepted")
	}
	if len(runner.Calls) != before {
		t.Fatal("missing exact session opened transport")
	}
}

func TestAPIExitCategories(t *testing.T) {
	for status, want := range map[int]int{400: 2, 401: 3, 403: 3, 409: 4, 429: 5, 502: 5, 0: 5} {
		if got := ExitCode(&APIError{Status: status}); got != want {
			t.Fatalf("%d: %d", status, got)
		}
	}
}
