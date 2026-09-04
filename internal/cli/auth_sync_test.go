package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestAuthSyncIncludesActiveGitHubCredentialAndIdentity(t *testing.T) {
	home := t.TempDir()
	p := newCLIProvider()
	p.boxes["worker"] = provider.Box{ID: "worker", Name: "worker", State: provider.StateRunning}
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte("github.com\n  ✓ Logged in to github.com account octocat (keyring)\n  - Active account: true\n  - Git operations protocol: ssh\n")},
		{Stdout: []byte("github-secret\n")},
		{Stdout: []byte("octocat\n")},
		{Stdout: []byte("Octo Cat\n")},
		{Stdout: []byte("octocat@example.invalid\n")},
	}}
	app := New()
	var stderr bytes.Buffer
	app.Err = &stderr
	app.Environ = map[string]string{"HOME": home}
	app.Runner = runner
	if err := app.syncApplicationProfiles(context.Background(), p, []string{"worker"}); err != nil {
		t.Fatal(err)
	}
	var setup boxruntime.SetupRequest
	for _, call := range p.exec {
		if reflect.DeepEqual(call.argv, []string{"vmbox-runtime", "setup"}) {
			if err := json.Unmarshal(call.data, &setup); err != nil {
				t.Fatal(err)
			}
		}
	}
	if setup.GitHub == nil || setup.GitHub.Host != "github.com" || setup.GitHub.User != "octocat" || setup.GitHub.Protocol != "ssh" || setup.GitHub.Token != "github-secret" || setup.GitHub.Name != "Octo Cat" || setup.GitHub.Email != "octocat@example.invalid" {
		t.Fatalf("GitHub setup=%+v", setup.GitHub)
	}
	if strings.Contains(stderr.String(), "github-secret") {
		t.Fatalf("credential leaked to progress output: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "selected GitHub credential octocat@github.com (ssh)") || !strings.Contains(stderr.String(), "GitHub credential is ready") {
		t.Fatalf("progress=%q", stderr.String())
	}
	if !reflect.DeepEqual(runner.Calls[1].Argv, []string{"gh", "auth", "token", "--hostname", "github.com", "--user", "octocat"}) {
		t.Fatalf("token lookup=%#v", runner.Calls)
	}
	if !reflect.DeepEqual(p.exec[len(p.exec)-1].argv, []string{"gh", "auth", "status", "--hostname", "github.com"}) {
		t.Fatalf("remote GitHub verification=%#v", p.exec)
	}
}

func TestAuthSyncCanExplicitlySkipGitHub(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".codex")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "auth.json"), []byte(`{"token":"codex-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	p := newCLIProvider()
	p.boxes["worker"] = provider.Box{ID: "worker", Name: "worker", State: provider.StateRunning}
	runner := &procexec.FakeRunner{}
	app := New()
	app.Err = &bytes.Buffer{}
	app.Environ = map[string]string{"HOME": home, "CODEX_HOME": profile}
	app.Runner = runner
	if err := app.syncApplicationProfiles(context.Background(), p, []string{"worker", "--no-github"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.Calls) != 0 {
		t.Fatalf("--no-github queried GitHub: %#v", runner.Calls)
	}
	for _, call := range p.exec {
		if reflect.DeepEqual(call.argv, []string{"vmbox-runtime", "setup"}) {
			var setup boxruntime.SetupRequest
			if err := json.Unmarshal(call.data, &setup); err != nil {
				t.Fatal(err)
			}
			if setup.GitHub != nil {
				t.Fatalf("--no-github uploaded GitHub setup: %+v", setup.GitHub)
			}
		}
	}
}

func TestAuthSyncDoesNotReportRootOnlyGitHubCredentialReady(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte("github-secret\n")},
		{Stdout: []byte("octocat\n")},
		{Stdout: []byte("Octo Cat\n")},
		{Stdout: []byte("octocat@example.invalid\n")},
	}}
	app := New()
	var stderr bytes.Buffer
	app.Err = &stderr
	app.Runner = runner
	setup := config.CreationSetup{
		Workspace: "/data/workspace",
		GitHub:    &config.GitHubCredential{Host: "github.com", User: "octocat", Protocol: "https"},
	}
	execute := func(_ context.Context, argv []string, _ provider.ExecOptions) (provider.ExecResult, error) {
		if reflect.DeepEqual(argv, []string{"vmbox-runtime", "setup"}) {
			return provider.ExecResult{Stdout: `{"authentication":{}}`}, nil
		}
		if reflect.DeepEqual(argv, []string{"gh", "auth", "status", "--hostname", "github.com"}) {
			return provider.ExecResult{ExitCode: 1}, nil
		}
		return provider.ExecResult{}, nil
	}
	err := app.uploadSelectedAuthentication(context.Background(), "worker", setup, execute)
	if err == nil || !strings.Contains(err.Error(), "verify GitHub authentication as workload user") {
		t.Fatalf("root-only GitHub credential accepted: %v", err)
	}
	if strings.Contains(stderr.String(), "GitHub credential is ready") {
		t.Fatalf("false-positive readiness output: %q", stderr.String())
	}
}

func TestSelectActiveGitHubCredentialRejectsAmbiguity(t *testing.T) {
	_, err := selectActiveGitHubCredential([]githubAccount{
		{Host: "github.com", User: "one", Protocol: "https", Active: true},
		{Host: "example.com", User: "two", Protocol: "ssh", Active: true},
	})
	if err == nil || !strings.Contains(err.Error(), "multiple active") {
		t.Fatalf("error=%v", err)
	}
}

// TestAuthSyncWarnsWhenAnAgentRejectsTheUploadedLogin is the operator-visible
// half of the stale-credential failure. The box completed the setup protocol
// normally — exit status zero, a well-formed result — and reported the agent as
// not logged in. That verdict has to reach the operator as a warning: a box
// whose synced credential is stale looks identical to a working one until
// someone opens it, which is exactly how the production box was handed over.
func TestAuthSyncWarnsWhenAnAgentRejectsTheUploadedLogin(t *testing.T) {
	app := New()
	var stderr bytes.Buffer
	app.Err = &stderr
	app.Runner = &procexec.FakeRunner{}
	prepared := preparedSetup{
		setup:        config.CreationSetup{Workspace: "/data/workspace"},
		applications: []string{"claude"},
	}
	execute := func(_ context.Context, argv []string, _ provider.ExecOptions) (provider.ExecResult, error) {
		if reflect.DeepEqual(argv, []string{"vmbox-runtime", "setup"}) {
			return provider.ExecResult{Stdout: `{"authentication":{"claude":false}}`}, nil
		}
		return provider.ExecResult{}, nil
	}
	if err := app.uploadPreparedWith(context.Background(), "worker", prepared, execute); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "claude authentication is ready") {
		t.Fatalf("stale credential was reported as ready: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning: claude did not recognize the uploaded login") {
		t.Fatalf("stale credential produced no warning: %q", stderr.String())
	}
}
