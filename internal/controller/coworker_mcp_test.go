package controller

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCoworkerMCPProtocol(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`, 200},
		{`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, 200},
		{`{"jsonrpc":"2.0","method":"notifications/initialized"}`, 202},
		{`{"jsonrpc":"2.0","method":"tools/call"}`, 400},
		{`{"jsonrpc":"2.0","id":3,"method":"ping"} {}`, 400},
		{`{"jsonrpc":"2.0","id":null,"method":"ping"}`, 400},
		{`{"jsonrpc":"2.0","id":true,"method":"ping"}`, 400},
		{`{"jsonrpc":"2.0","id":{},"method":"ping"}`, 400},
		{`{"jsonrpc":"2.0","id":1.2,"method":"ping"}`, 400},
		{`{"jsonrpc":"2.0","id":"request-1","method":"ping"}`, 200},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/mcp/coworkers", strings.NewReader(tc.body))
		s.coworkerMCP(w, r, CoworkerIdentity{})
		if w.Code != tc.status {
			t.Fatalf("status=%d want=%d", w.Code, tc.status)
		}
		if w.Code == 200 && !json.Valid(w.Body.Bytes()) {
			t.Fatal("invalid protocol result")
		}
	}
}

func TestCoworkerToolArgumentsAreObjectsAndRequireRevision(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `"text"`, `{} {}`} {
		if decodeCoworkerArgs([]byte(raw), &struct{}{}) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	s := &Server{}
	for _, raw := range []string{`{"action":"create","taskId":"task","title":"test"}`, `{"revision":null,"action":"create","taskId":"task","title":"test"}`} {
		if _, err := s.callCoworkerTool(httptest.NewRequest("POST", "/mcp/coworkers", nil), CoworkerIdentity{}, "board_edit", []byte(raw)); err == nil {
			t.Fatal("missing revision accepted")
		}
	}
}

func TestCoworkerMCPRejectsBrowserOriginBeforeAuthentication(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/mcp/coworkers", nil)
	r.Header.Set("Origin", "https://untrusted.example")
	s.coworkerAuth(s.coworkerMCP)(w, r)
	if w.Code != 403 {
		t.Fatal("browser origin accepted")
	}
}

func TestCoworkerHibernateCannotSelectAnotherBox(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("POST", "/mcp/coworkers", nil)
	for _, raw := range []string{`{"completed":false}`, `{"completed":true,"boxId":"another-box"}`} {
		if _, err := s.callCoworkerTool(r, CoworkerIdentity{BoxID: "self"}, "hibernate_self", []byte(raw)); err == nil {
			t.Fatal("unsafe hibernate arguments accepted")
		}
	}
}
