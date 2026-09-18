package boxruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// A profile that already disables approvals must end up with the box's value,
// not two conflicting ones.
func TestEnsureCodexDefaultsReplacesConflictingPermissions(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	existing := "approval_policy = \"on-request\"\nsandbox_mode = \"workspace-write\"\nmodel = \"o3\"\n"
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCodexDefaults(home, ""); err != nil {
		t.Fatal(err)
	}
	config := readCodexConfig(t, home)
	if strings.Contains(config, "on-request") || strings.Contains(config, "workspace-write") {
		t.Fatalf("stale permissions survived:\n%s", config)
	}
	if !strings.Contains(config, `model = "o3"`) {
		t.Fatalf("unrelated setting lost:\n%s", config)
	}
}

func TestCodexThreadIDPrefersNewestRolloutForWorkspaceAndRemembersIt(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	workspace := "/data/workspaces/box/workspace"
	writeRollout(t, home, "aaaaaaaa-0000-0000-0000-000000000001", workspace)
	writeRollout(t, home, "bbbbbbbb-0000-0000-0000-000000000002", "/somewhere/else")
	newest := "cccccccc-0000-0000-0000-000000000003"
	writeRollout(t, home, newest, workspace)

	id, err := CodexThreadID(root, home, "codex-chat", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if id != newest {
		t.Fatalf("thread id = %q, want the newest rollout for this workspace (%q)", id, newest)
	}
	// The id is remembered, so a later delivery does not rescan and cannot drift
	// onto a thread started afterwards.
	drift := "dddddddd-0000-0000-0000-000000000004"
	writeRollout(t, home, drift, workspace)
	again, err := CodexThreadID(root, home, "codex-chat", workspace)
	if err != nil || again != newest {
		t.Fatalf("remembered id = %q err=%v, want %q", again, err, newest)
	}
	if err := ForgetCodexThread(root, "codex-chat"); err != nil {
		t.Fatal(err)
	}
	if after, err := CodexThreadID(root, home, "codex-chat", workspace); err != nil || after != drift {
		t.Fatalf("after forgetting, id = %q err=%v, want %q", after, err, drift)
	}
}

func TestCodexThreadIDReportsWhenNoThreadMatches(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	writeRollout(t, home, "aaaaaaaa-0000-0000-0000-000000000001", "/other/workspace")
	if _, err := CodexThreadID(root, home, "codex-chat", "/data/workspaces/box/workspace"); err == nil {
		t.Fatal("a workspace with no recorded thread must be reported, not guessed")
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

var rolloutClock int

func writeRollout(t *testing.T, home, id, workspace string) {
	t.Helper()
	dir := filepath.Join(home, ".codex", "sessions", "2026", "09", "18")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-09-18T03-34-39-"+id+".jsonl")
	body := `{"type":"session_meta","payload":{"session_id":"` + id + `","cwd":"` + workspace + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// Newest wins, so each rollout gets a distinct, increasing time rather than
	// relying on filesystem timestamp granularity.
	rolloutClock++
	stamp := time.Now().Add(time.Duration(rolloutClock) * time.Second)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

// A Codex that has not run a turn yet records no thread. Delivery must fall back
// to its terminal rather than reporting failure, which is what sent the previous
// implementation off to start a second Codex.
func TestDeliverCodexChatTypesIntoTerminalWhenNoThreadRecorded(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	originalQueue, originalCommand := runCodexQueue, tmuxCommand
	originalSettle, originalSubmit, originalConfirm := agentReadySettlePause, tmuxSubmitPause, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		runCodexQueue, tmuxCommand = originalQueue, originalCommand
		agentReadySettlePause, tmuxSubmitPause, tmuxSubmitConfirmPause = originalSettle, originalSubmit, originalConfirm
	})
	agentReadySettlePause = func(context.Context) error { return nil }
	tmuxSubmitPause = func(context.Context) error { return nil }
	tmuxSubmitConfirmPause = func(context.Context) error { return nil }
	queued := false
	runCodexQueue = func(context.Context, string, string, []string) error { queued = true; return nil }
	var buffered string
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "capture-pane" {
			return []byte("OpenAI Codex\n\u203a Ask Codex to do anything"), nil
		}
		// The text is staged first, then a separate buffer submits it.
		if len(args) > 0 && args[0] == "load-buffer" && buffered == "" {
			buffered = stdin
		}
		return nil, nil
	}
	if err := DeliverCodexChat(context.Background(), root, home, "codex-chat", ChatInbound{ID: "message-1", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if queued {
		t.Fatal("queued against a thread that does not exist")
	}
	if buffered != "hello" {
		t.Fatalf("message typed into the terminal = %q", buffered)
	}
}
