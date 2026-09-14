package boxruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeliverTmuxInputReturnsSuccessForPersistedDelivery(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "messages")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "message_1.delivered"), []byte("done\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := DeliverTmuxInput(context.Background(), root, "vmbox", "message_1", "Y Z ä ö ü ß @ € | \\ [ ] { } ~", true); err != nil {
		t.Fatal(err)
	}
}

func TestDeliverTmuxInputRefusesAmbiguousReplay(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "messages")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "message_2.pending"), []byte("started\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := DeliverTmuxInput(context.Background(), root, "vmbox", "message_2", "do not replay", true)
	if !errors.Is(err, ErrAmbiguousMessage) {
		t.Fatalf("error=%v", err)
	}
}

func TestDeliverTmuxKeysUsesAllowlistedTmuxKeyEvents(t *testing.T) {
	originalCommand := tmuxCommand
	t.Cleanup(func() { tmuxCommand = originalCommand })
	var call []string
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if stdin != "" {
			t.Fatalf("key delivery wrote stdin %q", stdin)
		}
		call = append([]string(nil), args...)
		return nil, nil
	}
	root := t.TempDir()
	if err := DeliverTmuxKeys(context.Background(), root, "codex", "keys_1", []string{"Up", "Enter", "C-C"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"send-keys", "-t", "codex", "Up", "Enter", "C-C"}
	if strings.Join(call, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("tmux call=%q want=%q", call, want)
	}
	if _, err := os.Stat(filepath.Join(root, "messages", "keys_1.delivered")); err != nil {
		t.Fatalf("delivery marker: %v", err)
	}
}

func TestDeliverTmuxKeysRejectsArbitraryTmuxArguments(t *testing.T) {
	originalCommand := tmuxCommand
	t.Cleanup(func() { tmuxCommand = originalCommand })
	tmuxCommand = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		t.Fatal("unsupported key reached tmux")
		return nil, nil
	}
	if err := DeliverTmuxKeys(context.Background(), t.TempDir(), "codex", "keys_2", []string{"run-shell"}); err == nil {
		t.Fatal("arbitrary tmux argument was accepted")
	}
}

func TestDeliverTmuxInputWaitsAfterPasteBeforeSubmit(t *testing.T) {
	originalCommand, originalPause, originalConfirm := tmuxCommand, tmuxSubmitPause, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		tmuxCommand, tmuxSubmitPause, tmuxSubmitConfirmPause = originalCommand, originalPause, originalConfirm
	})
	paused := false
	submitted := false
	tmuxSubmitPause = func(context.Context) error {
		paused = true
		return nil
	}
	tmuxSubmitConfirmPause = func(context.Context) error { return nil }
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "send-keys" {
			t.Fatal("task submit used tmux's symbolic key instead of a terminal byte")
		}
		if len(args) > 0 && args[0] == "load-buffer" && stdin == "\r" {
			if !paused {
				t.Fatal("carriage return was loaded before the TUI paste pause")
			}
			submitted = true
		}
		if len(args) > 0 && args[0] == "capture-pane" {
			return []byte("────────────────\n❯\u00a0\n────────────────"), nil
		}
		return nil, nil
	}
	if err := DeliverTmuxInput(context.Background(), t.TempDir(), "claude", "message_submit", "hello", true); err != nil {
		t.Fatal(err)
	}
	if !paused || !submitted {
		t.Fatalf("pause=%v submitted=%v", paused, submitted)
	}
}

func TestDeliverTmuxInputAcceptsBashCarriageReturnWithoutTUIPrompt(t *testing.T) {
	originalCommand, originalPause := tmuxCommand, tmuxSubmitPause
	t.Cleanup(func() { tmuxCommand, tmuxSubmitPause = originalCommand, originalPause })
	tmuxSubmitPause = func(context.Context) error { return nil }
	carriageReturns := 0
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if args[0] == "display-message" {
			return []byte("bash\n"), nil
		}
		if args[0] == "capture-pane" {
			return []byte("worker$ printf hello\nhello\nworker$ "), nil
		}
		if args[0] == "load-buffer" && stdin == "\r" {
			carriageReturns++
		}
		return nil, nil
	}
	root := t.TempDir()
	if err := DeliverTmuxInput(context.Background(), root, "vmbox", "shell_message", "printf hello", true); err != nil {
		t.Fatal(err)
	}
	if carriageReturns != 1 {
		t.Fatalf("carriage returns=%d, want exactly one", carriageReturns)
	}
	if _, err := os.Stat(filepath.Join(root, "messages", "shell_message.delivered")); err != nil {
		t.Fatalf("shell command not recorded as delivered: %v", err)
	}
}

func TestDeliverTmuxInputRetriesOnlySubmitWhileClaudeInputIsStaged(t *testing.T) {
	originalCommand, originalPause, originalConfirm := tmuxCommand, tmuxSubmitPause, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		tmuxCommand, tmuxSubmitPause, tmuxSubmitConfirmPause = originalCommand, originalPause, originalConfirm
	})
	tmuxSubmitPause = func(context.Context) error { return nil }
	tmuxSubmitConfirmPause = func(context.Context) error { return nil }
	carriageReturns, captures := 0, 0
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if stdin == "hello" {
			return nil, nil
		}
		if stdin == "\r" {
			carriageReturns++
			return nil, nil
		}
		if len(args) > 0 && args[0] == "capture-pane" {
			captures++
			if captures == 2 {
				return []byte("Claude is repainting"), nil
			}
			if captures < 4 {
				return []byte("Claude Code v2\n────────────────\n❯\u00a0hello\n────────────────"), nil
			}
			return []byte("❯ hello\n● Working\n────────────────\n❯\u00a0\n────────────────"), nil
		}
		return nil, nil
	}
	if err := DeliverTmuxInput(context.Background(), t.TempDir(), "claude", "message_retry", "hello", true); err != nil {
		t.Fatal(err)
	}
	if carriageReturns != 2 {
		t.Fatalf("carriage returns=%d, want 2 without replaying prompt text", carriageReturns)
	}
}

func TestDeliverTmuxInputLeavesStagedTextBehindCodexUpdatePrompt(t *testing.T) {
	originalCommand, originalPause := tmuxCommand, tmuxSubmitPause
	t.Cleanup(func() { tmuxCommand, tmuxSubmitPause = originalCommand, originalPause })
	tmuxSubmitPause = func(context.Context) error { return nil }
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "capture-pane" {
			return []byte("Update available! 0.153.0 -> 0.153.2\n1. Update now\n2. Skip\n3. Skip until next version\nPress enter to continue"), nil
		}
		if stdin == "\r" || len(args) > 0 && args[0] == "send-keys" {
			t.Fatal("update prompt was submitted instead of surfaced to the controller")
		}
		return nil, nil
	}
	root := t.TempDir()
	if err := DeliverTmuxInput(context.Background(), root, "codex", "message_update", "hello", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "messages", "message_update.delivered")); err != nil {
		t.Fatalf("staged task was not recorded: %v", err)
	}
}

func TestTmuxInteractionRejectsUnsafeNames(t *testing.T) {
	for _, value := range []string{"", "../other", "name:window", "bad name", "ä"} {
		if err := validateTmuxToken("session", value); err == nil {
			t.Errorf("unsafe value %q accepted", value)
		}
	}
}

func TestStartTmuxTaskRejectsAnExistingSessionForAnotherAgent(t *testing.T) {
	original := tmuxCommand
	t.Cleanup(func() { tmuxCommand = original })
	var calls []string
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if len(args) > 0 && args[0] == "show-environment" {
			return []byte("VMBOX_TASK_AGENT=shell\n"), nil
		}
		return nil, nil
	}
	err := StartTmuxTask(context.Background(), t.TempDir(), "vmbox", "claude", "message_3", "hello")
	if err == nil || !strings.Contains(err.Error(), "choose a different session name") {
		t.Fatalf("error=%v calls=%v", err, calls)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "load-buffer") || strings.HasPrefix(call, "paste-buffer") {
			t.Fatalf("prompt was pasted into the wrong session: %v", calls)
		}
	}
}

func TestStartTmuxTaskWaitsForCodexInputBeforeDeliveringPrompt(t *testing.T) {
	originalCommand, originalInterval, originalTimeout, originalSettle, originalConfirm := tmuxCommand, agentReadyPollInterval, agentReadyTimeout, agentReadySettlePause, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		tmuxCommand, agentReadyPollInterval, agentReadyTimeout, agentReadySettlePause, tmuxSubmitConfirmPause = originalCommand, originalInterval, originalTimeout, originalSettle, originalConfirm
	})
	agentReadyPollInterval = 0
	agentReadyTimeout = time.Second
	agentReadySettlePause = func(context.Context) error { return nil }
	tmuxSubmitConfirmPause = func(context.Context) error { return nil }
	var calls []string
	captures := 0
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if strings.HasPrefix(call, "has-session") {
			return nil, errors.New("missing")
		}
		if strings.HasPrefix(call, "capture-pane") {
			captures++
			if captures == 1 {
				return []byte("starting"), nil
			}
			return []byte("OpenAI Codex\n› Ask Codex to do anything"), nil
		}
		return nil, nil
	}
	if err := StartTmuxTask(context.Background(), t.TempDir(), "codex-ready", "codex", "message_4", "hello"); err != nil {
		t.Fatal(err)
	}
	if captures < 2 {
		t.Fatalf("prompt was delivered before readiness: %v", calls)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "-- codex -c check_for_update_on_startup=false") {
		t.Fatalf("managed Codex session may intercept the initial prompt with an update menu: %v", calls)
	}
}

func TestCodexUpdateMenuIsNotInputReady(t *testing.T) {
	content := "OpenAI Codex\n› Ask Codex to do anything\nUpdate available!\nSkip until next version\nPress enter to continue"
	if agentInputReady("codex", content) {
		t.Fatal("update menu was treated as ready for a task prompt")
	}
}

func TestStartTmuxTaskAcceptsClaudeTrustBeforeDeliveringPrompt(t *testing.T) {
	originalCommand, originalInterval, originalTimeout, originalSettle, originalConfirm := tmuxCommand, agentReadyPollInterval, agentReadyTimeout, agentReadySettlePause, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		tmuxCommand, agentReadyPollInterval, agentReadyTimeout, agentReadySettlePause, tmuxSubmitConfirmPause = originalCommand, originalInterval, originalTimeout, originalSettle, originalConfirm
	})
	agentReadyPollInterval = 0
	agentReadyTimeout = time.Second
	agentReadySettlePause = func(context.Context) error { return nil }
	tmuxSubmitConfirmPause = func(context.Context) error { return nil }
	var calls []string
	captures := 0
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if strings.HasPrefix(call, "has-session") {
			return nil, errors.New("missing")
		}
		if strings.HasPrefix(call, "capture-pane") {
			captures++
			if captures == 1 {
				return []byte("Quick safety check:\n❯ No, exit\n  Yes, I trust this folder\nEnter to confirm"), nil
			}
			return []byte("Claude Code v2\n❯\u00a0Try \"write a test for <filepath>\""), nil
		}
		return nil, nil
	}
	if err := StartTmuxTask(context.Background(), t.TempDir(), "claude-ready", "claude", "message_5", "hello"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "new-session -d -s claude-ready -c /data/workspace -- env DISABLE_AUTOUPDATER=1 claude") {
		t.Fatalf("managed Claude task did not disable background self-update: %v", calls)
	}
	trust := strings.Index(joined, "send-keys -t claude-ready Down Enter")
	delivery := strings.Index(joined, "load-buffer")
	if trust < 0 || delivery < 0 || trust >= delivery {
		t.Fatalf("trust was not accepted before prompt delivery: %v", calls)
	}
}

func TestClaudeInputReadinessRejectsBareStartupPrompt(t *testing.T) {
	if agentInputReady("claude", "Claude Code v2.1.259\n❯") {
		t.Fatal("bare Claude startup prompt was accepted before the real input placeholder")
	}
	if !agentInputReady("claude", "Claude Code v2.1.259\n❯\u00a0Try \"fix lint errors\"") {
		t.Fatal("Claude real input placeholder was not recognized")
	}
}

func TestCaptureTmuxScreenRejectsMissingOrWrongSessionMetadata(t *testing.T) {
	original := captureTmuxCommand
	t.Cleanup(func() { captureTmuxCommand = original })
	for _, metadata := range []string{
		"\x1f\x1f\x1f\x1f\x1f\n",
		"other-session\x1f%1\x1ftitle\x1fclaude\x1f80\x1f24\n",
	} {
		captureCalls := 0
		captureTmuxCommand = func(_ context.Context, args ...string) ([]byte, error) {
			captureCalls++
			return []byte(metadata), nil
		}
		if _, err := CaptureTmuxScreen(context.Background(), "expected-session", 100); err == nil || !strings.Contains(err.Error(), "requested session") {
			t.Fatalf("metadata=%q error=%v", metadata, err)
		}
		if captureCalls != 1 {
			t.Fatalf("wrong pane was captured after mismatched metadata: calls=%d", captureCalls)
		}
	}
}

func TestSteerInterruptsBeforePasteAndDoesNotReplay(t *testing.T) {
	originalCommand, originalPause := tmuxCommand, tmuxSubmitPause
	t.Cleanup(func() { tmuxCommand = originalCommand; tmuxSubmitPause = originalPause })
	tmuxSubmitPause = func(context.Context) error { return nil }
	var calls []string
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "show-environment" {
			return []byte(taskAgentEnvironment + "=codex"), nil
		}
		return nil, nil
	}
	root := t.TempDir()
	if err := DeliverTmuxInput(context.Background(), root, "agent", "steer_1", "change direction", false, true); err != nil {
		t.Fatal(err)
	}
	interrupt, paste := -1, -1
	for i, call := range calls {
		if strings.HasPrefix(call, "send-keys") {
			interrupt = i
		}
		if strings.HasPrefix(call, "paste-buffer") {
			paste = i
		}
	}
	if interrupt < 0 || paste < interrupt {
		t.Fatal("steer did not interrupt before pasting")
	}
	n := len(calls)
	if err := DeliverTmuxInput(context.Background(), root, "agent", "steer_1", "change direction", false, true); err != nil || len(calls) != n {
		t.Fatal("idempotent steer replayed")
	}
}
