package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestLegacyProviderSelectionDoesNotFilterBoxInventory(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/logical-boxes" || r.URL.RawQuery != "" {
			t.Errorf("filtered inventory request: %s", r.URL.String())
		}
		_ = json.NewEncoder(w).Encode([]v1.LogicalBox{
			{Name: "railway-box", Provider: "railway", State: v1.LogicalBoxRunning},
			{Name: "shared-box", Provider: "shared-worker", State: v1.LogicalBoxHibernated},
		})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "config.json")
	legacy := config.Context{Name: "team", Controller: server.URL, TokenEnv: "TEST_TOKEN", Provider: "railway", ProviderCredential: "primary"}
	if err := config.Save(path, config.File{Current: "team", Contexts: map[string]config.Context{"team": legacy}}); err != nil {
		t.Fatal(err)
	}
	app := New()
	app.ConfigPath = path
	app.Environ = map[string]string{"TEST_TOKEN": "synthetic"}
	app.IsTerminal = func() bool { return false }
	app.Err = &bytes.Buffer{}
	var output bytes.Buffer
	app.Out = &output
	if err := app.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"railway-box", "shared-box"} {
		if !strings.Contains(output.String(), name) {
			t.Fatalf("overview omitted %s: %s", name, output.String())
		}
	}
	output.Reset()
	if err := app.Run(context.Background(), []string{"ls", "--json"}); err != nil {
		t.Fatal(err)
	}
	var boxes []v1.LogicalBox
	if err := json.Unmarshal(output.Bytes(), &boxes); err != nil || len(boxes) != 2 {
		t.Fatalf("JSON inventory has %d boxes: %v", len(boxes), err)
	}
	if requests != 2 {
		t.Fatalf("inventory requests = %d", requests)
	}
}

func TestLegacyPoolCannotOverrideControllerDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/controller-defaults":
			_ = json.NewEncoder(w).Encode(v1.FleetConfig{Provider: "shared-worker", ProviderCredential: "spacevm"})
		case "/v1/fleet/status":
			if r.URL.Query().Get("provider") != "shared-worker" || r.URL.Query().Get("providerCredential") != "spacevm" {
				t.Errorf("wrong worker pool: %s", r.URL.String())
			}
			_ = json.NewEncoder(w).Encode(v1.FleetStatus{Provider: "shared-worker", ProviderCredential: "spacevm"})
		default:
			t.Errorf("unexpected request: %s", r.URL.String())
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.File{Current: "team", Contexts: map[string]config.Context{"team": {Controller: server.URL, TokenEnv: "TEST_TOKEN", Provider: "railway", ProviderCredential: "primary"}}}); err != nil {
		t.Fatal(err)
	}
	app := New()
	app.ConfigPath = path
	app.Environ = map[string]string{"TEST_TOKEN": "synthetic"}
	app.IsTerminal = func() bool { return false }
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	if err := app.Run(context.Background(), []string{"fleet", "status", "--json"}); err != nil {
		t.Fatal(err)
	}
}

func TestConnectMigratesSelectedLoginAndLogoutClearsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := config.Context{Name: "team", Controller: "https://controller.example", TokenEnv: "TEAM_TOKEN", Provider: "railway", LocationPresets: map[string]string{"shared-worker/spacevm": "eu"}}
	if err := config.Save(path, config.File{Current: "team", Contexts: map[string]config.Context{"team": legacy}}); err != nil {
		t.Fatal(err)
	}
	app := New()
	app.ConfigPath = path
	app.Environ = map[string]string{}
	app.IsTerminal = func() bool { return false }
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	oldPath := app.legacyTokenPath(legacy)
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("synthetic-login"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.Run(context.Background(), []string{"connect", legacy.Controller}); err != nil {
		t.Fatal(err)
	}
	file, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	connected, err := file.Connected()
	if err != nil || connected.Name != "" || connected.Provider != "" || connected.TokenEnv != "TEAM_TOKEN" || connected.LocationPresets["shared-worker/spacevm"] != "eu" {
		t.Fatalf("connection migration: %+v, %v", connected, err)
	}
	if token, err := app.savedControllerToken(connected); err != nil || token != "synthetic-login" {
		t.Fatalf("saved login not migrated: %v", err)
	}
	if err := app.Run(context.Background(), []string{"logout"}); err != nil {
		t.Fatal(err)
	}
	for _, tokenPath := range []string{oldPath, app.tokenPath(connected)} {
		if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
			t.Fatalf("logout retained a saved login: %v", err)
		}
	}
}

func TestReusableSetupMigratesFromSelectedLegacyContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	directory := t.TempDir()
	file := config.File{Current: "team", Contexts: map[string]config.Context{
		"team": {Controller: "https://controller.example"},
	}}
	setup := config.CreationSetup{Version: 1, Workspace: "/data/workspace"}
	if err := saveSetup(path, file, "team", directory, setup); err != nil {
		t.Fatal(err)
	}
	file, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSetup(file, "https://controller.example", directory)
	if err != nil || loaded.Workspace != setup.Workspace {
		t.Fatalf("legacy reusable setup: %+v, %v", loaded, err)
	}
	if _, err := loadSetup(file, "https://different.example", directory); err == nil {
		t.Fatal("reused setup across controllers")
	}
}

func TestPoolsCommandKeepsProvidersAlias(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/provider-credentials" {
			t.Errorf("unexpected request: %s", r.URL.String())
		}
		requests++
		_ = json.NewEncoder(w).Encode([]v1.ProviderCredential{{Provider: "shared-worker", Name: "spacevm"}})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.File{Connection: &config.ControllerConnection{Controller: server.URL, TokenEnv: "TEST_TOKEN"}}); err != nil {
		t.Fatal(err)
	}
	app := New()
	app.ConfigPath = path
	app.Environ = map[string]string{"TEST_TOKEN": "synthetic"}
	app.IsTerminal = func() bool { return false }
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	for _, command := range []string{"pools", "providers"} {
		if err := app.Run(context.Background(), []string{command, "list"}); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 2 || !strings.Contains(app.Out.(*bytes.Buffer).String(), "spacevm") {
		t.Fatalf("worker pool commands: requests=%d output=%q", requests, app.Out.(*bytes.Buffer).String())
	}
}

func TestExplicitPoolTargetsCreationWithoutControllerDefault(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/logical-boxes" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			return
		}
		var request v1.CreateLogicalBoxRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Provider != "shared-worker" || request.ProviderCredential != "spacevm" {
			t.Errorf("wrong worker pool: %+v", request)
		}
		_ = json.NewEncoder(w).Encode(v1.LogicalBox{ID: "box-id", Name: request.Name, State: v1.LogicalBoxHibernated})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.File{Connection: &config.ControllerConnection{Controller: server.URL, TokenEnv: "TEST_TOKEN"}}); err != nil {
		t.Fatal(err)
	}
	app := New()
	app.ConfigPath = path
	app.Environ = map[string]string{"TEST_TOKEN": "synthetic"}
	app.IsTerminal = func() bool { return false }
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	if err := app.Run(context.Background(), []string{"new", "work", "--pool", "shared-worker/spacevm", "--hibernate", "--no-dialog"}); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("creation requests = %d", requests)
	}
}

func TestExplicitPoolTargetsFleetWithoutControllerDefault(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/fleet/status" || r.URL.Query().Get("provider") != "shared-worker" || r.URL.Query().Get("providerCredential") != "spacevm" {
			t.Errorf("wrong fleet target: %s", r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(v1.FleetStatus{Provider: "shared-worker", ProviderCredential: "spacevm"})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.File{Connection: &config.ControllerConnection{Controller: server.URL, TokenEnv: "TEST_TOKEN"}}); err != nil {
		t.Fatal(err)
	}
	app := New()
	app.ConfigPath = path
	app.Environ = map[string]string{"TEST_TOKEN": "synthetic"}
	app.IsTerminal = func() bool { return false }
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	if err := app.Run(context.Background(), []string{"fleet", "status", "--pool", "shared-worker/spacevm", "--json"}); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("fleet requests = %d", requests)
	}
}

func TestRunPoolOptionDoesNotConsumeCommandArguments(t *testing.T) {
	args := []string{"job", "--pool", "shared-worker/spacevm", "--", "echo", "--pool", "literal"}
	opts, err := parseRunOptions(args)
	if err != nil || opts.pool != "shared-worker/spacevm" || len(opts.argv) != 3 || opts.argv[1] != "--pool" || !hasWorkerPoolOption(args) {
		t.Fatalf("run options: %+v, %v", opts, err)
	}
	if hasWorkerPoolOption([]string{"job", "--", "echo", "--pool", "literal"}) {
		t.Fatal("command argv was mistaken for a worker pool option")
	}
}

func TestControllerURLSecretsAreRejectedWithoutPrintingThem(t *testing.T) {
	const secret = "private-sentinel"
	for _, tc := range []struct{ name, address string }{
		{"userinfo", "https://user:" + secret + "@controller.example"},
		{"query", "https://controller.example?token=" + secret},
		{"fragment", "https://controller.example#" + secret},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			app := New()
			app.ConfigPath = path
			app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
			err := app.Run(context.Background(), []string{"connect", tc.address})
			if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(app.Out.(*bytes.Buffer).String(), secret) || strings.Contains(app.Err.(*bytes.Buffer).String(), secret) {
				t.Fatalf("secret-bearing URL was accepted or printed: %v", err)
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatalf("invalid connection was saved: %v", statErr)
			}
		})
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.File{Connection: &config.ControllerConnection{Controller: "https://user:" + secret + "@controller.example"}}); err != nil {
		t.Fatal(err)
	}
	app := New()
	app.ConfigPath = path
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	for _, args := range [][]string{nil, {"connect"}} {
		err := app.Run(context.Background(), args)
		if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(app.Out.(*bytes.Buffer).String(), secret) || strings.Contains(app.Err.(*bytes.Buffer).String(), secret) {
			t.Fatalf("saved secret-bearing URL was accepted or printed by %v: %v", args, err)
		}
	}
}
