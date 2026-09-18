package boxruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCodexDefaultsWritesPermissionsAndTrust(t *testing.T) {
	home := t.TempDir()
	if err := EnsureCodexDefaults(home, "/data/workspaces/box/workspace"); err != nil {
		t.Fatal(err)
	}
	config := readCodexConfig(t, home)
	for _, want := range []string{
		`approval_policy = "never"`,
		`sandbox_mode = "danger-full-access"`,
		`[projects."/data/workspaces/box/workspace"]`,
		`trust_level = "trusted"`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("missing %q in:\n%s", want, config)
		}
	}
}

// An imported login profile ships its own config.toml. Applying the box defaults
// over it must keep the user's settings and their other trusted projects.
func TestEnsureCodexDefaultsPreservesImportedProfile(t *testing.T) {
	home := t.TempDir()
	imported := `model = "gpt-5.6-sol"
model_reasoning_effort = "high"

[projects."/home/user/other"]
trust_level = "trusted"

[tui.model_availability_nux]
gpt-6-astra = 2
`
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(imported), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCodexDefaults(home, "/data/workspaces/box/workspace"); err != nil {
		t.Fatal(err)
	}
	config := readCodexConfig(t, home)
	for _, want := range []string{
		`model = "gpt-5.6-sol"`,
		`model_reasoning_effort = "high"`,
		`[projects."/home/user/other"]`,
		`[tui.model_availability_nux]`,
		`approval_policy = "never"`,
		`sandbox_mode = "danger-full-access"`,
		`[projects."/data/workspaces/box/workspace"]`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("missing %q in:\n%s", want, config)
		}
	}
}

// Re-applying must not accumulate duplicates: it runs on every attach.
func TestEnsureCodexDefaultsIsIdempotent(t *testing.T) {
	home := t.TempDir()
	workspace := "/data/workspaces/box/workspace"
	for range 3 {
		if err := EnsureCodexDefaults(home, workspace); err != nil {
			t.Fatal(err)
		}
	}
	config := readCodexConfig(t, home)
	for _, key := range []string{`approval_policy = "never"`, `sandbox_mode = "danger-full-access"`, `[projects."` + workspace + `"]`} {
		if got := strings.Count(config, key); got != 1 {
			t.Fatalf("%q appears %d times in:\n%s", key, got, config)
		}
	}
}
func readCodexConfig(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDeliverCodexChatTypesLiteralKeysWhenNoThreadRecorded(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	originalQueue, originalCommand, originalSubmit := runCodexQueue, tmuxCommand, tmuxSubmitPause
	t.Cleanup(func() { runCodexQueue, tmuxCommand, tmuxSubmitPause = originalQueue, originalCommand, originalSubmit })
	tmuxSubmitPause = func(context.Context) error { return nil }
	queued := false
	runCodexQueue = func(context.Context, string, string, []string) error { queued = true; return nil }
	var calls [][]string
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if len(args) > 0 && args[0] == "capture-pane" {
			return []byte("OpenAI Codex\n\u203a Ask Codex to do anything"), nil
		}
		return nil, nil
	}
	if err := DeliverCodexChat(context.Background(), root, home, "codex-chat", ChatInbound{ID: "message-1", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if queued {
		t.Fatal("queued against a thread that does not exist")
	}
	var typed, submitted bool
	for _, call := range calls {
		if len(call) >= 5 && call[0] == "send-keys" && call[3] == "-l" && call[4] == "hello" {
			typed = true
		}
		if len(call) >= 4 && call[0] == "send-keys" && call[3] == "Enter" {
			submitted = typed
		}
	}
	if !typed {
		t.Fatalf("message was not sent as literal keys: %v", calls)
	}
	if !submitted {
		t.Fatalf("message was not submitted after being typed: %v", calls)
	}
	for _, call := range calls {
		if len(call) > 0 && (call[0] == "load-buffer" || call[0] == "paste-buffer") {
			t.Fatalf("used bracketed paste, which Codex ignores: %v", calls)
		}
	}
}
