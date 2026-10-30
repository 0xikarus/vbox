package cli

import (
	"bytes"
	"context"
	"encoding/json"
	hubservice "github.com/0xikarus/vmbox-service/internal/hub"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestHubCLIBypassesFleetAndKeepsCredentialsPrivate(t *testing.T) {
	token := strings.Repeat("a", 64)
	c := hubservice.Config{ID: "hub-" + strings.Repeat("b", 20), APIToken: token, Listen: "127.0.0.1:8091"}
	data, _ := json.Marshal(map[string]any{"dir": t.TempDir(), "config": c})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing hub authentication")
		}
		if r.URL.Path != "/api/boxes" {
			t.Error("wrong hub route")
		}
		w.Write([]byte(`[]`))
	}))
	defer server.Close()
	a := New()
	var out bytes.Buffer
	a.Out = &out
	a.Err = &out
	a.ConfigPath = filepath.Join(t.TempDir(), "missing-fleet.json")
	a.Runner = &procexec.FakeRunner{Results: []procexec.Result{{Stdout: data}}}
	a.IsTerminal = func() bool { return false }
	if err := a.Run(context.Background(), []string{"hub", "list", "--url", server.URL}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "[]\n" || strings.Contains(out.String(), token) {
		t.Fatal("CLI leaked host credentials")
	}
}
func TestHubDeleteRequiresExplicitFlag(t *testing.T) {
	a := New()
	a.IsTerminal = func() bool { return false }
	a.Out = &bytes.Buffer{}
	a.Err = &bytes.Buffer{}
	c := hubservice.Config{ID: "hub-" + strings.Repeat("b", 20), APIToken: strings.Repeat("a", 64), Listen: "127.0.0.1:8091"}
	data, _ := json.Marshal(map[string]any{"config": c})
	a.Runner = &procexec.FakeRunner{Results: []procexec.Result{{Stdout: data}}}
	if err := a.Run(context.Background(), []string{"hub", "delete", "hb-fixture"}); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal("permanent deletion lacked confirmation flag")
	}
}

func TestRemoteHubUsesSSHTransport(t *testing.T) {
	a := New()
	var out bytes.Buffer
	a.Out = &out
	a.Err = &out
	a.IsTerminal = func() bool { return false }
	c := hubservice.Config{ID: "hub-" + strings.Repeat("b", 20), APIToken: strings.Repeat("a", 64), Listen: "0.0.0.0:8091"}
	cfg, _ := json.Marshal(map[string]any{"config": c, "dir": "/private/hub"})
	r := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: cfg}, {Stdout: []byte(`{"status":200,"body":[]}`)}}}
	a.Runner = r
	if err := a.Run(context.Background(), []string{"hub", "list", "--host", "test-host"}); err != nil {
		t.Fatal(err)
	}
	if len(r.Calls) != 2 || r.Calls[1].Argv[0] != "ssh" || strings.Contains(r.Calls[1].Stdin, c.APIToken) || out.String() != "[]\n" {
		t.Fatal("remote management exposed credentials or bypassed SSH")
	}
}
