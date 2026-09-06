package cli

import (
	"bytes"
	"context"
	"encoding/json"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreationGitHubDiscoveryAndSelectedExport(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte("github.com\n  ✓ Logged in to github.com account alice (keyring)\n  - Active account: true\n  ✓ Logged in to github.com account bob (keyring)\n  - Active account: false\n")}, {Stdout: []byte("synthetic-selected-token\n")}}}
	a := New()
	a.Runner = runner
	a.Environ = map[string]string{"HOME": t.TempDir()}
	var screen bytes.Buffer
	a.Err = &screen
	profiles, err := a.discoverCreationLogins(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.addCreationGitHubAccounts(context.Background(), profiles)
	p := profiles[2]
	if len(p.localPaths) != 2 || p.selection.Value != "Skip" || len(runner.Calls) != 1 {
		t.Fatal("discovery exported or selected a credential")
	}
	p.selection.Value = "Local: bob@github.com"
	p.selection.OnSelect(p.selection.Value)
	if p.name.Value != "bob" {
		t.Fatalf("profile name = %q", p.name.Value)
	}
	p.selection.Choices = append(p.selection.Choices, "Saved: bob")
	p.selection.OnSelect(p.selection.Value)
	if p.name.Value != "bob-2" {
		t.Fatalf("collision name = %q", p.name.Value)
	}
	if p.uploadPath() != "github.com:bob" {
		t.Fatal("wrong selected account")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/v1/login-profiles/github/work" {
			t.Error("wrong target")
		}
		var req v1.SaveLoginProfileRequest
		json.NewDecoder(r.Body).Decode(&req)
		var c loginprofile.GitHub
		json.Unmarshal(req.Files["credential.json"], &c)
		if c.User != "bob" || c.Token != "synthetic-selected-token" || len(req.Files) != 1 {
			t.Error("wrong selected export")
		}
		json.NewEncoder(w).Encode(v1.LoginProfile{Application: "github", Name: "work"})
	}))
	defer server.Close()
	if _, err = a.saveLocalLoginProfile(context.Background(), config.Context{Controller: server.URL}, "test", "github", "work", p.uploadPath()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(runner.Calls[1].Argv, " ") != "gh auth token --hostname github.com --user bob" {
		t.Fatal("wrong keychain lookup")
	}
	if strings.Contains(screen.String(), "synthetic-selected-token") {
		t.Fatal("secret exposed")
	}
}
