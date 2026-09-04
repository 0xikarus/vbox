package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type providerExecCall struct {
	name string
	argv []string
	data []byte
}

type cliProvider struct {
	boxes   map[string]provider.Box
	created []provider.CreateRequest
	exec    []providerExecCall
	resized []string
	start   []string
}

type progressBootstrapProvider struct {
	*cliProvider
	delay time.Duration
}

type sessionCLIProvider struct {
	*cliProvider
	attached []string
}

type cancelledSessionCLIProvider struct {
	*cliProvider
}

func (p *cancelledSessionCLIProvider) AttachSession(ctx context.Context, _ string, _ string, _ []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	<-ctx.Done()
	return provider.ExecResult{ExitCode: -1}, ctx.Err()
}

func (p *sessionCLIProvider) AttachSession(_ context.Context, name, session string, command []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	p.attached = append(p.attached, name+":"+session+":"+strings.Join(command, " "))
	return provider.ExecResult{}, nil
}

func (p *progressBootstrapProvider) Bootstrap(ctx context.Context, _ string, _ provider.BootstrapRequest) error {
	select {
	case <-time.After(p.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newCLIProvider() *cliProvider  { return &cliProvider{boxes: make(map[string]provider.Box)} }
func (p *cliProvider) Name() string { return "test" }
func (p *cliProvider) Validate(context.Context) (provider.Capabilities, error) {
	return provider.Capabilities{}, nil
}
func (p *cliProvider) Create(_ context.Context, req provider.CreateRequest) (provider.Box, error) {
	p.created = append(p.created, req)
	box := provider.Box{ID: req.Name, Name: req.Name, Provider: p.Name(), State: provider.StateRunning, Region: req.Region, Resources: req.Resources, Owner: req.Owner, Connection: provider.Connection{Transport: "test-exec", Endpoint: req.Name}, Storage: &provider.Storage{MountPath: "/data", SizeGiB: req.Resources.DiskGiB}}
	p.boxes[req.Name] = box
	return box, nil
}
func (p *cliProvider) Inspect(_ context.Context, id string) (provider.Box, error) {
	box, ok := p.boxes[id]
	if !ok {
		return box, provider.ErrNotFound
	}
	return box, nil
}
func (p *cliProvider) List(context.Context) ([]provider.Box, error) {
	result := make([]provider.Box, 0, len(p.boxes))
	for _, box := range p.boxes {
		result = append(result, box)
	}
	return result, nil
}
func (p *cliProvider) Start(_ context.Context, id string) (provider.Box, error) {
	p.start = append(p.start, id)
	box, err := p.Inspect(context.Background(), id)
	box.State = provider.StateRunning
	p.boxes[id] = box
	return box, err
}
func (p *cliProvider) Stop(_ context.Context, id string) (provider.Box, error) {
	return p.Inspect(context.Background(), id)
}
func (p *cliProvider) Resize(_ context.Context, id string, resources provider.Resources) (provider.Box, error) {
	p.resized = append(p.resized, id)
	box, err := p.Inspect(context.Background(), id)
	box.Resources = resources
	p.boxes[id] = box
	return box, err
}
func (p *cliProvider) Delete(context.Context, string, provider.Owner) error { return nil }
func (p *cliProvider) CreateStorage(context.Context, string, provider.Resources) (provider.Storage, error) {
	return provider.Storage{}, nil
}
func (p *cliProvider) AttachStorage(context.Context, string, provider.Storage) error { return nil }
func (p *cliProvider) DeleteStorage(context.Context, provider.Storage, provider.Owner) error {
	return nil
}
func (p *cliProvider) Deploy(_ context.Context, id, _ string) (provider.Box, error) {
	return p.Inspect(context.Background(), id)
}
func (p *cliProvider) Connection(_ context.Context, id string) (provider.Connection, error) {
	box, err := p.Inspect(context.Background(), id)
	return box.Connection, err
}
func (p *cliProvider) Logs(context.Context, string, provider.LogOptions, io.Writer) error { return nil }
func (p *cliProvider) Usage(context.Context, string) (provider.Usage, error) {
	return provider.Usage{}, nil
}
func (p *cliProvider) Exec(_ context.Context, id string, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	var data []byte
	if opts.Stdin != nil {
		data, _ = io.ReadAll(opts.Stdin)
	}
	p.exec = append(p.exec, providerExecCall{name: id, argv: append([]string(nil), argv...), data: data})
	result := provider.ExecResult{}
	if len(argv) >= 2 && argv[0] == "vmbox-runtime" && argv[1] == "put-file" {
		digest := sha256.Sum256(data)
		result.Stdout = fmt.Sprintf("%x\n", digest[:])
	}
	if reflect.DeepEqual(argv, []string{"vmbox-runtime", "sync-files"}) {
		digest := sha256.Sum256(data)
		result.Stdout = fmt.Sprintf("%x\n", digest[:])
	}
	if reflect.DeepEqual(argv, []string{"vmbox-runtime", "setup"}) {
		var request boxruntime.SetupRequest
		_ = json.Unmarshal(data, &request)
		authentication := make(map[string]bool)
		for _, application := range request.Applications {
			authentication[application] = true
		}
		encoded, _ := json.Marshal(boxruntime.SetupResult{Authentication: authentication})
		result.Stdout = string(encoded)
	}
	return result, nil
}
func (p *cliProvider) Reconcile(_ context.Context, box provider.Box) (provider.Box, error) {
	return box, nil
}

func TestSplitRunPreservesArgv(t *testing.T) {
	name, detach, reuse, argv, err := splitRun([]string{"worker", "--detach", "--reuse", "--", "tool", "--flag", "two words", "$HOME;"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "worker" || !detach || !reuse || !reflect.DeepEqual(argv, []string{"tool", "--flag", "two words", "$HOME;"}) {
		t.Fatalf("name=%q detach=%v reuse=%v argv=%#v", name, detach, reuse, argv)
	}
}

func TestNonTerminalRunExecutesDirectlyWithoutTmux(t *testing.T) {
	p := newCLIProvider()
	p.boxes["worker"] = provider.Box{ID: "worker", Name: "worker", State: provider.StateRunning, Owner: provider.Owner{AccountID: "standalone", BoxID: "worker"}}
	app := New()
	app.In, app.Out, app.Err = strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return false }
	file := config.File{Contexts: map[string]config.Context{"test": {Name: "test", Provider: "test"}}}
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"run", "worker", "--", "printf", "ok"}); err != nil {
		t.Fatal(err)
	}
	if len(p.exec) == 0 || !reflect.DeepEqual(p.exec[len(p.exec)-1].argv, []string{"printf", "ok"}) {
		t.Fatalf("nonterminal command was wrapped in an interactive session: %#v", p.exec)
	}
}

func TestControllerInitRequiresExplicitConfirmation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	file := config.File{Current: "prod", Contexts: map[string]config.Context{"prod": {Provider: "railway", Project: "p", Environment: "e", Image: "registry/box@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
	if err := config.Save(path, file); err != nil {
		t.Fatal(err)
	}
	runner := &procexec.FakeRunner{}
	var out bytes.Buffer
	app := New()
	app.ConfigPath, app.Runner, app.Out, app.Err = path, runner, &out, &bytes.Buffer{}
	err := app.Run(context.Background(), []string{"controller", "init", "--source", "../..", "--endpoint", "https://controller.example"})
	if err == nil || !strings.Contains(err.Error(), "billable") {
		t.Fatalf("error=%v", err)
	}
	if len(runner.Calls) != 0 || !strings.Contains(out.String(), "Controller plan") {
		t.Fatalf("calls=%d output=%q", len(runner.Calls), out.String())
	}
}

func TestControllerInitEnsuresInfrastructureAndSavesContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	file := config.File{Current: "prod", Contexts: map[string]config.Context{"prod": {Provider: "railway", Project: "p", Environment: "e", Image: "registry/box@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TokenEnv: "TEST_CONTROLLER_TOKEN"}}}
	if err := config.Save(path, file); err != nil {
		t.Fatal(err)
	}
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer health.Close()
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(`[]`)},
		{},
		{},
		{Stdout: []byte(`[{"id":"controller-id","name":"vmbox-controller"},{"id":"database-id","name":"vmbox-postgres"}]`)},
		{Stdout: []byte(`{}`)},
	}}
	var out bytes.Buffer
	app := New()
	app.ConfigPath, app.Runner, app.Out, app.Err = path, runner, &out, &bytes.Buffer{}
	if err := app.Run(context.Background(), []string{"controller", "init", "--source", "../..", "--endpoint", health.URL, "--yes"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.Calls) < 14 {
		t.Fatalf("calls=%d: %#v", len(runner.Calls), runner.Calls)
	}
	wantPrefix := []string{"railway", "service", "list", "--json", "--project", "p", "--environment", "e"}
	if !reflect.DeepEqual(runner.Calls[0].Argv, wantPrefix) {
		t.Fatalf("list argv=%#v", runner.Calls[0].Argv)
	}
	for _, call := range runner.Calls {
		joined := strings.Join(call.Argv, " ")
		if strings.Contains(joined, " variable set ") {
			if call.Stdin == "" {
				t.Fatalf("controller variable write did not use stdin: %#v", call)
			}
			for _, arg := range call.Argv {
				if arg == call.Stdin {
					t.Fatalf("controller variable leaked into argv: %#v", call)
				}
			}
		}
	}
	updated, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Contexts["prod"].Controller != health.URL {
		t.Fatalf("context=%+v", updated.Contexts["prod"])
	}
	if !strings.Contains(out.String(), "shown once") || strings.Contains(string(mustRead(t, path)), "VMBOX_ENCRYPTION_KEY") {
		t.Fatalf("output/config mismatch: %q", out.String())
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestControllerSourceStagingUsesExactAllowlist(t *testing.T) {
	source := t.TempDir()
	for _, directory := range []string{"cmd", "internal"} {
		if err := os.Mkdir(filepath.Join(source, directory), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, directory, "source.go"), []byte("package source"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Dockerfile", ".dockerignore", "entrypoint.sh", "go.mod", "go.sum"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "Codex conversation.md"), []byte("must not upload"), 0600); err != nil {
		t.Fatal(err)
	}
	staged, cleanup, err := stageControllerSource(source)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(staged)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 7 {
		t.Fatalf("staged entries=%v", entries)
	}
	if _, err := os.Stat(filepath.Join(staged, "Codex conversation.md")); !os.IsNotExist(err) {
		t.Fatalf("dev log reached staged source: %v", err)
	}
	cleanup()
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged source was not removed: %v", err)
	}
}
func TestControllerFailureDoesNotFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	file := config.File{Current: "team", Contexts: map[string]config.Context{"team": {Provider: "docker", Controller: "http://127.0.0.1:1", TokenEnv: "TEST_CONTROLLER_TOKEN"}}}
	if err := config.Save(path, file); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	app := New()
	app.ConfigPath = path
	app.Out = &out
	app.Err = &stderr
	app.Environ = map[string]string{"TEST_CONTROLLER_TOKEN": "not-a-real-token"}
	err := app.Run(context.Background(), []string{"new", "box", "--detach", "--", "printf", "ok"})
	if err == nil || !strings.Contains(err.Error(), "no standalone fallback") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "accepted") {
		t.Fatal("run was accepted despite unavailable controller")
	}
}
func TestContextContainsNoAmbientToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	app := New()
	app.ConfigPath = path
	app.Environ = map[string]string{"RAILWAY_API_TOKEN": "super-secret"}
	app.Out = &bytes.Buffer{}
	app.Err = &bytes.Buffer{}
	if err := app.Run(context.Background(), []string{"context", "add", "railway", "--provider", "railway", "--project", "p", "--environment", "production"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("super-secret")) {
		t.Fatal("ambient token persisted in context")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func TestContextRejectsRemovedProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	app := New()
	app.ConfigPath = path
	app.Out = &bytes.Buffer{}
	app.Err = &bytes.Buffer{}
	err := app.Run(context.Background(), []string{"context", "add", "old", "--provider", "sevalla"})
	if err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("removed provider created config: %v", statErr)
	}
}

func TestCreateAliasesResumeExistingAndPersistReusableSetup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	file := config.File{Current: "test", Contexts: map[string]config.Context{"test": {Name: "test", Provider: "test"}}}
	p := newCLIProvider()
	app := New()
	app.ConfigPath, app.Out, app.Err = path, &bytes.Buffer{}, &bytes.Buffer{}
	app.WorkingDir = dir
	app.IsTerminal = func() bool { return false }
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"create", "worker", "--detach"}); err != nil {
		t.Fatal(err)
	}
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"new", "worker", "--detach"}); err != nil {
		t.Fatal(err)
	}
	if len(p.created) != 1 {
		t.Fatalf("create calls=%d", len(p.created))
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := loadSetup(saved, "test", dir)
	if err != nil {
		t.Fatal(err)
	}
	if reused.Version != 1 || reused.Resources.MemoryMiB != 4096 || reused.WorkingDirectory != dir {
		t.Fatalf("saved=%+v", reused)
	}
	last := p.exec[len(p.exec)-1].argv
	if !reflect.DeepEqual(last, []string{"sh", "-c", `tmux has-session -t vmbox 2>/dev/null || exec tmux new-session -d -s vmbox -c /data/workspace vmbox-runtime welcome`}) {
		t.Fatalf("resume argv=%#v", last)
	}
}

func TestClosingInteractiveClientReturnsCleanlyAndKeepsRemoteRunning(t *testing.T) {
	base := newCLIProvider()
	base.boxes["worker"] = provider.Box{ID: "worker", Name: "worker", State: provider.StateRunning, Owner: provider.Owner{AccountID: "standalone", BoxID: "worker"}}
	p := &cancelledSessionCLIProvider{cliProvider: base}
	app := New()
	var stderr bytes.Buffer
	app.In, app.Out, app.Err = strings.NewReader(""), &bytes.Buffer{}, &stderr
	app.IsTerminal = func() bool { return true }
	file := config.File{Contexts: map[string]config.Context{"test": {Name: "test", Provider: "test"}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.standalone(ctx, file, p, file.Contexts["test"], []string{"new", "worker"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "box and tmux session are still running") {
		t.Fatalf("stderr=%q", stderr.String())
	}
	if len(base.start) != 0 {
		t.Fatalf("client close mutated remote lifecycle: start calls=%v", base.start)
	}
}

func TestInteractiveResumeUsesProviderNativeTmuxSession(t *testing.T) {
	base := newCLIProvider()
	base.boxes["worker"] = provider.Box{ID: "worker", Name: "worker", State: provider.StateRunning, Owner: provider.Owner{AccountID: "standalone", BoxID: "worker"}}
	p := &sessionCLIProvider{cliProvider: base}
	app := New()
	app.In, app.Out, app.Err = strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return true }
	file := config.File{Contexts: map[string]config.Context{"test": {Name: "test", Provider: "test"}}}
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"new", "worker"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.attached, []string{"worker:vmbox:vmbox-runtime welcome"}) {
		t.Fatalf("attached=%#v", p.attached)
	}
}

func TestNewRepairsIncompleteOwnedServiceInsteadOfStartingIt(t *testing.T) {
	dir := t.TempDir()
	p := newCLIProvider()
	p.boxes["worker"] = provider.Box{ID: "worker", Name: "worker", State: provider.StateStopped, Owner: provider.Owner{AccountID: "standalone"}}
	app := New()
	app.ConfigPath, app.WorkingDir = filepath.Join(dir, "config.json"), dir
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return false }
	file := config.File{Current: "test", Contexts: map[string]config.Context{"test": {Name: "test", Provider: "test"}}}
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"new", "worker", "--detach", "--component", "bun"}); err != nil {
		t.Fatal(err)
	}
	if len(p.created) != 1 || len(p.start) != 0 {
		t.Fatalf("create calls=%d start calls=%d", len(p.created), len(p.start))
	}
	if p.created[0].Owner.BoxID != "worker" || !strings.Contains(app.Err.(*bytes.Buffer).String(), "repairing incomplete Railway service") {
		t.Fatalf("request=%+v stderr=%q", p.created[0], app.Err.(*bytes.Buffer).String())
	}
}

func TestTaskStatusForwardsOptionalRuntimeRunID(t *testing.T) {
	p := newCLIProvider()
	p.boxes["worker"] = provider.Box{ID: "worker", Name: "worker", State: provider.StateRunning}
	app := New()
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	file := config.File{Contexts: map[string]config.Context{"test": {Name: "test", Provider: "test"}}}
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"task-status", "worker", "run_123"}); err != nil {
		t.Fatal(err)
	}
	last := p.exec[len(p.exec)-1].argv
	if !reflect.DeepEqual(last, []string{"vmbox-runtime", "status", "run_123"}) {
		t.Fatalf("task-status argv=%#v", last)
	}
}

func TestReuseReloadsExplicitMarkdownPathsAndReportsMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	instructions := filepath.Join(dir, "PROJECT.md")
	if err := os.WriteFile(instructions, []byte("project rules\n"), 0600); err != nil {
		t.Fatal(err)
	}
	file := config.File{Current: "test", Contexts: map[string]config.Context{"test": {Name: "test", Provider: "test"}}}
	p := newCLIProvider()
	var stderr bytes.Buffer
	app := New()
	app.ConfigPath, app.Out, app.Err = path, &bytes.Buffer{}, &stderr
	app.WorkingDir = dir
	app.IsTerminal = func() bool { return false }
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"new", "one", "--detach", "--instructions", instructions, "--cpu", "4"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(instructions); err != nil {
		t.Fatal(err)
	}
	if err := app.standalone(context.Background(), loaded, p, file.Contexts["test"], []string{"new", "two", "--detach", "--reuse"}); err != nil {
		t.Fatal(err)
	}
	if p.created[1].Resources.CPU != 4 {
		t.Fatalf("reused resources=%+v", p.created[1].Resources)
	}
	if !strings.Contains(stderr.String(), "Markdown instruction file missing; skipped") {
		t.Fatalf("stderr=%q", stderr.String())
	}
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := loadSetup(reloaded, "test", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reused.Instructions, []string{instructions}) {
		t.Fatalf("instructions=%#v", reused.Instructions)
	}
}

func TestProfilesGitHubAndMarkdownUploadIndependently(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, ".codex-work")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "auth.json"), []byte(`{"token":"application-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	instructions := filepath.Join(dir, "RULES.md")
	if err := os.WriteFile(instructions, []byte("markdown-only\n"), 0600); err != nil {
		t.Fatal(err)
	}
	file := config.File{Current: "test", Contexts: map[string]config.Context{"test": {Name: "test", Provider: "test"}}}
	p := newCLIProvider()
	app := New()
	app.ConfigPath, app.Out, app.Err = filepath.Join(dir, "config.json"), &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return false }
	app.Runner = &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte("github-secret\n")}}}
	err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"new", "worker", "--detach", "--application-profile", "codex=" + profile, "--github-credential", "github.com:octocat:ssh", "--instructions", instructions})
	if err != nil {
		t.Fatal(err)
	}
	foundAuth, foundMarkdown, foundGitHub := false, false, false
	for _, call := range p.exec {
		joined := strings.Join(call.argv, " ")
		if reflect.DeepEqual(call.argv, []string{"vmbox-runtime", "sync-files"}) {
			var request boxruntime.SyncRequest
			if err := json.Unmarshal(call.data, &request); err != nil {
				t.Fatal(err)
			}
			for _, file := range request.Files {
				switch file.Path {
				case "/data/home/.codex/auth.json":
					foundAuth = string(file.Data) == `{"token":"application-secret"}`
				case "/data/workspace/AGENTS.md":
					foundMarkdown = strings.Contains(string(file.Data), "markdown-only") && !strings.Contains(string(file.Data), "application-secret")
				}
			}
		}
		if reflect.DeepEqual(call.argv, []string{"vmbox-runtime", "setup"}) {
			var request boxruntime.SetupRequest
			if err := json.Unmarshal(call.data, &request); err != nil {
				t.Fatal(err)
			}
			foundGitHub = request.GitHub != nil && request.GitHub.Token == "github-secret" && request.GitHub.User == "octocat" && !strings.Contains(joined, "github-secret")
		}
	}
	if !foundAuth || !foundMarkdown || !foundGitHub {
		t.Fatalf("auth=%v markdown=%v github=%v calls=%#v", foundAuth, foundMarkdown, foundGitHub, p.exec)
	}
	progress := app.Err.(*bytes.Buffer).String()
	for _, expected := range []string{"syncing 3 selected agent/instruction file(s)", "batching file 1/3", "syncing GitHub credential for octocat@github.com", "GitHub credential is ready", "configuring agent trust", "tmux session metadata is ready"} {
		if !strings.Contains(progress, expected) {
			t.Fatalf("progress missing %q: %s", expected, progress)
		}
	}
}

func TestBootstrapReportsSelectionHeartbeatAndCompletion(t *testing.T) {
	assets := t.TempDir()
	if err := os.WriteFile(filepath.Join(assets, "vmbox-entrypoint"), []byte("entrypoint"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "vmbox-runtime-linux-amd64"), []byte("runtime"), 0600); err != nil {
		t.Fatal(err)
	}
	p := &progressBootstrapProvider{cliProvider: newCLIProvider(), delay: 8 * time.Millisecond}
	app := New()
	var stderr bytes.Buffer
	app.Err = &stderr
	app.Environ = map[string]string{"VMBOX_RUNTIME_ASSET_DIR": assets}
	app.ProgressInterval = time.Millisecond
	if err := app.ensureBootstrap(context.Background(), p, "worker", []string{"codex", "bun"}, false); err != nil {
		t.Fatal(err)
	}
	progress := stderr.String()
	for _, expected := range []string{"bootstrapping \"worker\" (codex, bun)", "still bootstrapping \"worker\"", "runtime and tools are ready in \"worker\""} {
		if !strings.Contains(progress, expected) {
			t.Fatalf("progress missing %q: %s", expected, progress)
		}
	}
}

func TestAuthSyncCopiesActiveClaudeCredentialAndHomeState(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".claude")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, ".credentials.json"), []byte("claude-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("claude-home-state"), 0600); err != nil {
		t.Fatal(err)
	}
	p := newCLIProvider()
	p.boxes["worker"] = provider.Box{ID: "worker", Name: "worker", State: provider.StateRunning}
	app := New()
	var stderr bytes.Buffer
	app.Err = &stderr
	app.Environ = map[string]string{"HOME": home}
	if err := app.syncApplicationProfiles(context.Background(), p, []string{"worker"}); err != nil {
		t.Fatal(err)
	}
	foundCredential, foundHomeState, foundStatus := false, false, false
	for _, call := range p.exec {
		if reflect.DeepEqual(call.argv, []string{"vmbox-runtime", "sync-files"}) {
			var request boxruntime.SyncRequest
			if err := json.Unmarshal(call.data, &request); err != nil {
				t.Fatal(err)
			}
			for _, file := range request.Files {
				if file.Path == "/data/home/.claude/.credentials.json" {
					foundCredential = string(file.Data) == "claude-credential"
				}
				if file.Path == "/data/home/.claude.json" {
					foundHomeState = string(file.Data) == "claude-home-state"
				}
			}
		}
		if reflect.DeepEqual(call.argv, []string{"vmbox-runtime", "setup"}) {
			var request boxruntime.SetupRequest
			if err := json.Unmarshal(call.data, &request); err != nil {
				t.Fatal(err)
			}
			foundStatus = reflect.DeepEqual(request.Applications, []string{"claude"})
		}
	}
	if !foundCredential || !foundHomeState || !foundStatus {
		t.Fatalf("credential=%v home-state=%v status=%v calls=%#v", foundCredential, foundHomeState, foundStatus, p.exec)
	}
	if !strings.Contains(stderr.String(), "claude authentication is ready") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestUnifiedScreenAndSelectorsRequireVisibleConfirm(t *testing.T) {
	app := New()
	var out bytes.Buffer
	app.In, app.Out, app.Err = strings.NewReader("\nq"), &out, &bytes.Buffer{}
	app.Runner = &procexec.FakeRunner{}
	_, err := app.configureSetup(context.Background(), config.Context{Name: "test", Provider: "docker"}, nil, "worker", defaultSetup(config.Context{}), []string{"printf", "two words"})
	if !errors.Is(err, errSetupCancelled) {
		t.Fatalf("Enter outside Confirm accepted setup: %v", err)
	}
	if !strings.Contains(out.String(), "Enter: Cancel/Confirm") || !strings.Contains(out.String(), "[ Cancel ]") || !strings.Contains(out.String(), "[ Confirm and create ]") || !strings.Contains(out.String(), `["printf","two words"]`) {
		t.Fatalf("screen=%q", out.String())
	}

	p := newCLIProvider()
	p.boxes["worker"] = provider.Box{ID: "worker", Name: "worker", State: provider.StateRunning}
	app.In, app.Out = strings.NewReader(" j\n"), &bytes.Buffer{}
	app.IsTerminal = func() bool { return true }
	name, err := app.selectStandaloneBox(context.Background(), p, "Select")
	if err != nil || name != "worker" {
		t.Fatalf("name=%q err=%v", name, err)
	}
	app.In = strings.NewReader("\njjj\n")
	resources, err := app.selectResizeResources("Resize")
	if err != nil || resources.CPU != 2 || resources.MemoryMiB != 4096 {
		t.Fatalf("resources=%+v err=%v", resources, err)
	}
}

func TestUnifiedScreenPreservesExplicitCustomResources(t *testing.T) {
	app := New()
	var out bytes.Buffer
	app.In, app.Out, app.Err = strings.NewReader("q"), &out, &bytes.Buffer{}
	app.Runner = &procexec.FakeRunner{}
	setup := defaultSetup(config.Context{})
	setup.Resources = provider.Resources{CPU: 1, MemoryMiB: 512, DiskGiB: 7}
	_, err := app.configureSetup(context.Background(), config.Context{Name: "test", Provider: "docker"}, nil, "worker", setup, nil)
	if !errors.Is(err, errSetupCancelled) {
		t.Fatalf("configureSetup error = %v", err)
	}
	if !strings.Contains(out.String(), "[x] Custom") || !strings.Contains(out.String(), "1 CPU / 512 MiB / 7 GiB") {
		t.Fatalf("screen=%q", out.String())
	}
}

func TestCreationCancellationHappensBeforeProviderMutation(t *testing.T) {
	for name, input := range map[string]string{"q": "q", "uppercase-q": "Q", "escape": "\x1b", "ctrl-c": "\x03", "visible-cancel": "kk\n"} {
		t.Run(name, func(t *testing.T) {
			p := newCLIProvider()
			app := New()
			app.In, app.Out, app.Err = strings.NewReader(input), &bytes.Buffer{}, &bytes.Buffer{}
			app.IsTerminal = func() bool { return true }
			app.Runner = &procexec.FakeRunner{}
			app.Environ = map[string]string{"HOME": t.TempDir()}
			file := config.File{Contexts: map[string]config.Context{"test": {Name: "test", Provider: "test"}}}
			if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"new", "worker"}); err != nil {
				t.Fatal(err)
			}
			if len(p.created) != 0 || len(p.exec) != 0 {
				t.Fatalf("provider mutated before confirmation: creates=%d exec=%d", len(p.created), len(p.exec))
			}
		})
	}
}

func TestWelcomeContainsSpecsConnectionCostAndDetachInstructions(t *testing.T) {
	box := provider.Box{Name: "worker", Provider: "docker", Region: "local", State: provider.StateRunning, Resources: provider.Resources{CPU: 2, MemoryMiB: 4096}, Connection: provider.Connection{Transport: "docker-exec", Endpoint: "default"}, Storage: &provider.Storage{MountPath: "/data", SizeGiB: 10}}
	text := string(welcome(box, "local-docker", "unavailable"))
	for _, required := range []string{"2 CPU", "4096 MiB", "10 GiB", "docker-exec default", "Cost: unavailable", "Ctrl-a", "release both keys", "press d"} {
		if !strings.Contains(text, required) {
			t.Fatalf("welcome missing %q: %s", required, text)
		}
	}
}

func TestResizeWithoutNameUsesSelector(t *testing.T) {
	p := newCLIProvider()
	p.boxes["worker"] = provider.Box{ID: "worker", Name: "worker", State: provider.StateRunning}
	app := New()
	app.In, app.Out, app.Err = strings.NewReader(" j\n"), &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return true }
	file := config.File{Contexts: map[string]config.Context{"test": {Name: "test", Provider: "test"}}}
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"resize", "--cpu", "3", "--memory", "6144"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.resized, []string{"worker"}) {
		t.Fatalf("resized=%#v", p.resized)
	}
}

func TestControllerCreateResumesExistingBoxWithoutSubmittingRun(t *testing.T) {
	createdRuns, starts := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs":
			json.NewEncoder(w).Encode([]v1.Run{{ID: "run-1", State: v1.JobRunning, Request: v1.CreateRunRequest{Box: "worker"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run-1/start":
			starts++
			json.NewEncoder(w).Encode(provider.Box{Name: "worker", State: provider.StateRunning})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			createdRuns++
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(v1.Run{ID: "unexpected"})
		default:
			http.Error(w, r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()
	app := New()
	app.Environ = map[string]string{"TOKEN": "secret"}
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	c := config.Context{Name: "team", Provider: "docker", Controller: server.URL, TokenEnv: "TOKEN"}
	if err := app.controller(context.Background(), config.File{}, c, []string{"new", "worker"}); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || createdRuns != 0 {
		t.Fatalf("starts=%d created=%d", starts, createdRuns)
	}
}

func TestControllerResizeWithoutNameUsesSelector(t *testing.T) {
	resized := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs":
			json.NewEncoder(w).Encode([]v1.Run{{ID: "run-1", State: v1.JobRunning, Request: v1.CreateRunRequest{Box: "worker"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run-1/resize":
			var resources provider.Resources
			json.NewDecoder(r.Body).Decode(&resources)
			resized = resources.CPU == 3 && resources.MemoryMiB == 6144
			json.NewEncoder(w).Encode(provider.Box{Name: "worker", Resources: resources})
		default:
			http.Error(w, r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()
	app := New()
	app.Environ = map[string]string{"TOKEN": "secret"}
	app.In, app.Out, app.Err = strings.NewReader(" j\n"), &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return true }
	c := config.Context{Name: "team", Provider: "docker", Controller: server.URL, TokenEnv: "TOKEN"}
	if err := app.controller(context.Background(), config.File{}, c, []string{"resize", "--cpu", "3", "--memory", "6144"}); err != nil {
		t.Fatal(err)
	}
	if !resized {
		t.Fatal("selected controller run was not resized with exact limits")
	}
}
