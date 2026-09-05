package cli

import (
	"bytes"
	"context"
	"encoding/json"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrimarySelectionAndReconnect(t *testing.T) {
	fence := strings.Repeat("a", 64)
	first := v1.Session{ID: "$1", Name: "old session ü", Incarnation: "inc-1"}
	second := v1.Session{ID: "$2", Name: "second", Incarnation: "inc-2"}
	primary := ""
	writes := 0
	created := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/logical-boxes/box-id/sessions":
			json.NewEncoder(w).Encode(v1.SessionInventory{State: "live", Assignment: fence, Sessions: []v1.Session{first, second}})
		case "/v1/logical-boxes/box-id/sessions/primary":
			if r.Method == "PUT" {
				var in struct {
					SessionID   string `json:"sessionId"`
					Incarnation string `json:"incarnation"`
				}
				json.NewDecoder(r.Body).Decode(&in)
				if in.SessionID == first.ID && in.Incarnation == first.Incarnation {
					primary = first.Name
				} else if in.SessionID == second.ID && in.Incarnation == second.Incarnation {
					primary = second.Name
				} else {
					t.Fatal("wrong live selection")
				}
				writes++
			}
			json.NewEncoder(w).Encode(map[string]string{"session": primary})
		case "/v1/logical-boxes/box-id/native-connection":
			s := first
			if r.URL.Query().Get("sessionId") == second.ID {
				s = second
			}
			json.NewEncoder(w).Encode(v1.NativeConnection{LogicalBoxConnection: v1.LogicalBoxConnection{LogicalBoxID: "box-id", Connection: provider.Connection{Transport: "openssh", Endpoint: "user@host"}}, Assignment: fence, SessionID: s.ID, Incarnation: s.Incarnation})
		default:
			created++
			t.Error("unexpected mutation", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	open := func(keys, session string, force bool) *App {
		a := New()
		a.In = strings.NewReader(keys)
		a.Out = &bytes.Buffer{}
		a.Err = &bytes.Buffer{}
		a.IsTerminal = func() bool { return true }
		a.Runner = &procexec.FakeRunner{Results: []procexec.Result{{ExitCode: 255}}}
		err := a.openInteractive(context.Background(), config.Context{Controller: server.URL}, "test", v1.LogicalBox{ID: "box-id", Name: "helper1"}, session, "", force)
		if err == nil || !strings.Contains(err.Error(), "not replayed") {
			t.Fatal(err)
		}
		return a
	}
	a := open("\x1b[B\r", "", true)
	if primary != second.Name || writes != 1 || !strings.Contains(a.Err.(*bytes.Buffer).String(), "\x1b[?1049h") {
		t.Fatal(primary, writes)
	}
	a = open("", "", false)
	if writes != 1 || strings.Contains(a.Err.(*bytes.Buffer).String(), "\x1b[?1049h") {
		t.Fatal("remembered primary prompted or rewrote preference")
	}
	open("", first.Name, false)
	if primary != first.Name || writes != 2 {
		t.Fatal("explicit name not remembered")
	}
	if created != 0 {
		t.Fatal("selection created sessions")
	}
}

func TestBareSessionFlagDispatch(t *testing.T) {
	a := New()
	a.Environ = map[string]string{"TOKEN": "test"}
	a.IsTerminal = func() bool { return false }
	err := a.controller(context.Background(), config.File{}, config.Context{TokenEnv: "TOKEN"}, []string{"helper1", "--session"})
	if err == nil || !strings.Contains(err.Error(), "requires an interactive terminal") {
		t.Fatal(err)
	}
}
