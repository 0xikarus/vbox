package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type shellReuseProvider struct {
	fakeProvider
	names  []string
	starts [][]string
	fail   bool
}

func (p *shellReuseProvider) Exec(_ context.Context, _ string, argv []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	if p.fail {
		return provider.ExecResult{}, errors.New("SSH permission denied")
	}
	switch argv[1] {
	case "native-sessions":
		inv := v1.SessionInventory{State: "live", Assignment: argv[2]}
		for _, name := range p.names {
			inv.Sessions = append(inv.Sessions, v1.Session{Name: name})
		}
		b, _ := json.Marshal(inv)
		return provider.ExecResult{Stdout: string(b)}, nil
	case "interactive-start":
		p.starts = append(p.starts, append([]string(nil), argv...))
		p.names = append(p.names, argv[3])
		return provider.ExecResult{}, nil
	default:
		return provider.ExecResult{}, errors.New("unexpected runtime operation")
	}
}

// Called inside TestProcessPostgres's isolated schema. This verifies actual
// controller transactions and metadata; worker transport is deliberately fake.
func testInteractiveShellReuse(t *testing.T, s *Server, p Principal, box string) {
	prov := &shellReuseProvider{names: []string{"claude-existing"}}
	previous := s.Resolve
	s.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return prov, nil }
	defer func() { s.Resolve = previous }()
	call := func(body string, want int) string {
		t.Helper()
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.SetPathValue("id", box)
		w := httptest.NewRecorder()
		s.interactiveStartHandler(w, r, p)
		if w.Code != want {
			t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
		}
		var result struct{ Session string }
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result.Session
	}
	first := call(`{"agent":"shell","reuseShell":true}`, 201)
	if first == "" || len(prov.starts) != 1 || prov.names[0] != "claude-existing" {
		t.Fatal("missing shell or replaced legacy session")
	}
	if again := call(`{"agent":"shell","reuseShell":true}`, 200); again != first || len(prov.starts) != 1 {
		t.Fatal("reconnect started another process")
	}
	prov.fail = true
	call(`{"agent":"shell","reuseShell":true}`, 502)
	if len(prov.starts) != 1 {
		t.Fatal("SSH failure created a shell")
	}
	prov.fail = false
	prov.names = []string{"claude-existing"}
	replacement := call(`{"agent":"shell","reuseShell":true}`, 201)
	if replacement == first || len(prov.starts) != 2 {
		t.Fatal("missing shell was not replaced")
	}
	call(`{"agent":"shell","reuseShell":true,"startCli":"claude"}`, 400)
	call(`{"agent":"claude","startCli":"echo wrong"}`, 400)
	started := call(`{"agent":"shell","startCli":"./custom-script --flag"}`, 201)
	last := prov.starts[len(prov.starts)-1]
	if len(last) != 6 || last[5] != "./custom-script --flag" {
		t.Fatalf("startup argv=%q", last)
	}
	if again := call(`{"agent":"shell","reuseShell":true}`, 200); again != started || len(prov.starts) != 3 {
		t.Fatal("startup replayed on reconnect")
	}
}
