package boxruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Exercise tmux's actual format expansion: a fake returning tab-separated
// fields would hide an incorrectly escaped separator in the format argument.
func TestOpenCodeBridgeHealthWithRealTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("", "vbox-oc-bridge-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "tmux.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	run := func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "tmux", append([]string{"-S", socket, "-f", "/dev/null"}, args...)...).CombinedOutput()
	}
	if output, err := run(ctx, "", "new-session", "-d", "-s", "bridge", "sleep", "60"); err != nil {
		t.Fatalf("isolated tmux: %v: %s", err, output)
	}
	t.Cleanup(func() { _, _ = run(context.Background(), "", "kill-server") })
	original := tmuxCommand
	tmuxCommand = run
	t.Cleanup(func() { tmuxCommand = original })
	pane, err := run(ctx, "", "display-message", "-p", "-t", "=bridge:", "#{pane_id}")
	if err != nil {
		t.Fatal(err)
	}
	pid, err := run(ctx, "", "display-message", "-p", "-t", "=bridge:", "#{pane_pid}")
	if err != nil {
		t.Fatal(err)
	}
	rootPID, err := strconv.Atoi(strings.TrimSpace(string(pid)))
	if err != nil {
		t.Fatal(err)
	}
	identity := openCodeBridgeIdentity{Pane: strings.TrimSpace(string(pane)), PID: rootPID, Instance: "fixture-instance"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(identity)
	}))
	defer server.Close()
	// Preserve the bridge's request path while routing it to the fixture server.
	client := &http.Client{Transport: openCodeTestTransport(func(r *http.Request) (*http.Response, error) {
		return server.Client().Get(server.URL + r.URL.Path)
	})}
	got, err := openCodeBridgeHealth(ctx, client, "bridge")
	if err != nil || got != identity {
		t.Fatalf("healthy current pane rejected: got=%+v err=%v", got, err)
	}
	identity.Pane = "%99999"
	if _, err := openCodeBridgeHealth(ctx, client, "bridge"); err == nil || !strings.Contains(err.Error(), "stale pane") {
		t.Fatalf("stale pane not rejected: %v", err)
	}
	identity.Pane = strings.TrimSpace(string(pane))
	identity.PID = os.Getpid()
	if _, err := openCodeBridgeHealth(ctx, client, "bridge"); err == nil || !strings.Contains(err.Error(), "stale TUI process") {
		t.Fatalf("unrelated process not rejected: %v", err)
	}
}

func TestRegisterOpenCodeTUIPreservesSettings(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".config")
	configPath := filepath.Join(root, "opencode", "tui.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"theme":"existing","plugin":["custom-plugin"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := registerOpenCodeTUI(home, root); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Theme   string   `json:"theme"`
		Plugins []string `json:"plugin"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Theme != "existing" || len(config.Plugins) != 2 || config.Plugins[0] != "custom-plugin" {
		t.Fatalf("settings changed or plugin duplicated: %s", data)
	}
	if _, err := os.Stat(config.Plugins[1]); err != nil {
		t.Fatal(err)
	}
}
