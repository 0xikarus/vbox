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
	boxes         map[string]provider.Box
	created       []provider.CreateRequest
	exec          []providerExecCall
	resized       []string
	start         []string
	setupPending  map[string]bool
	setupFailures int
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

func newCLIProvider() *cliProvider {
	return &cliProvider{boxes: make(map[string]provider.Box), setupPending: make(map[string]bool)}
}
func (p *cliProvider) Name() string { return "test" }
func (p *cliProvider) Validate(context.Context) (provider.Capabilities, error) {
	return provider.Capabilities{}, nil
}
func (p *cliProvider) Create(_ context.Context, req provider.CreateRequest) (provider.Box, error) {
	p.created = append(p.created, req)
	if req.Env[standaloneSetupStateEnv] == "pending" {
		if p.setupPending == nil {
			p.setupPending = make(map[string]bool)
		}
		p.setupPending[req.Name] = true
	}
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
	if reflect.DeepEqual(argv, []string{"sh", "-c", standaloneSetupProbe}) {
		if p.setupPending[id] {
			result.Stdout = "pending\n"
		} else {
			result.Stdout = "complete\n"
		}
	}
	if len(argv) >= 2 && argv[0] == "vmbox-runtime" && argv[1] == "put-file" {
		digest := sha256.Sum256(data)
		result.Stdout = fmt.Sprintf("%x\n", digest[:])
		if len(argv) >= 3 && argv[2] == standaloneSetupMarker {
			if p.setupPending == nil {
				p.setupPending = make(map[string]bool)
			}
			p.setupPending[id] = false
		}
	}
	if reflect.DeepEqual(argv, []string{"vmbox-runtime", "sync-files"}) {
		digest := sha256.Sum256(data)
		result.Stdout = fmt.Sprintf("%x\n", digest[:])
	}
	if reflect.DeepEqual(argv, []string{"vmbox-runtime", "setup"}) {
		if p.setupFailures > 0 {
			p.setupFailures--
			result.ExitCode = 1
			return result, nil
		}
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

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
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
	err := app.Run(context.Background(), []string{"new", "box", "--detach"})
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
	if err := app.Run(context.Background(), []string{"context", "add", "team", "--controller", "https://controller.example"}); err != nil {
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
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("removed provider created config: %v", statErr)
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
	// New() installs the real OSRunner, so leaving it in place makes GitHub
	// discovery shell out to the developer's own `gh auth status`. This test is
	// about the Claude profile; an empty fake keeps its subject the only input.
	app.Runner = &procexec.FakeRunner{}
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
			// A GitHub credential here could only have come from the host, which
			// would mean the runner substitution above stopped working.
			if request.GitHub != nil {
				t.Fatalf("sync reached a GitHub account outside the test: %s@%s", request.GitHub.User, request.GitHub.Host)
			}
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

	app.IsTerminal = func() bool { return true }
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

func TestWelcomeContainsSpecsConnectionCostAndDetachInstructions(t *testing.T) {
	box := provider.Box{Name: "worker", Provider: "docker", Region: "local", State: provider.StateRunning, Resources: provider.Resources{CPU: 2, MemoryMiB: 4096}, Connection: provider.Connection{Transport: "docker-exec", Endpoint: "default"}, Storage: &provider.Storage{MountPath: "/data", SizeGiB: 10}}
	text := string(welcome(box, "local-docker", "unavailable"))
	for _, required := range []string{"2 CPU", "4096 MiB", "10 GiB", "docker-exec default", "Cost: unavailable", "Ctrl-a", "release both keys", "press d"} {
		if !strings.Contains(text, required) {
			t.Fatalf("welcome missing %q: %s", required, text)
		}
	}
}

func TestWelcomeUploadUsesPrivateHomeFileMode(t *testing.T) {
	p := newCLIProvider()
	box := provider.Box{Name: "worker", Provider: "test", State: provider.StateRunning}
	app := New()
	app.Err = io.Discard
	if err := app.uploadWelcome(context.Background(), p, box, "test"); err != nil {
		t.Fatal(err)
	}
	if len(p.exec) != 1 || !reflect.DeepEqual(p.exec[0].argv, []string{"vmbox-runtime", "put-file", "/data/home/.vmbox-welcome", "0600"}) {
		t.Fatalf("welcome upload=%#v", p.exec)
	}
}

func TestControllerCreateUsesLogicalBoxesWithoutSubmittingRun(t *testing.T) {
	logicalCreates, createdRuns := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/controller-defaults":
			json.NewEncoder(w).Encode(v1.FleetConfig{Provider: "docker"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/logical-boxes":
			logicalCreates++
			var request v1.CreateLogicalBoxRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Name != "worker" || request.AllocateWhenReady == nil || *request.AllocateWhenReady || request.DefaultAgent != "shell" {
				t.Fatalf("logical box request=%+v", request)
			}
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(v1.LogicalBox{ID: "box-1", Name: "worker", State: v1.LogicalBoxHibernated})
		case r.URL.Path == "/v1/logical-boxes/box-1/allocate":
			json.NewEncoder(w).Encode(v1.Allocation{RequestID: "allocation", State: "ready"})
		case r.URL.Path == "/v1/logical-boxes/box-1":
			json.NewEncoder(w).Encode(v1.LogicalBox{ID: "box-1", Name: "worker", State: v1.LogicalBoxRunning})
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
	if err := app.controller(context.Background(), config.File{}, c, []string{"new", "worker", "--detach"}); err != nil {
		t.Fatal(err)
	}
	if logicalCreates != 1 || createdRuns != 0 {
		t.Fatalf("logical creates=%d run creates=%d", logicalCreates, createdRuns)
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
