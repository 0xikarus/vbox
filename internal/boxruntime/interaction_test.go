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

func TestDeliverTmuxInputWaitsAfterPasteBeforeSubmit(t *testing.T) {
	originalCommand, originalPause := tmuxCommand, tmuxSubmitPause
	t.Cleanup(func() { tmuxCommand, tmuxSubmitPause = originalCommand, originalPause })
	paused := false
	tmuxSubmitPause = func(context.Context) error {
		paused = true
		return nil
	}
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "send-keys" && !paused {
			t.Fatal("Enter was sent before the TUI paste pause")
		}
		return nil, nil
	}
	if err := DeliverTmuxInput(context.Background(), t.TempDir(), "claude", "message_submit", "hello", true); err != nil {
		t.Fatal(err)
	}
	if !paused {
		t.Fatal("submit pause was not used")
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
	originalCommand, originalInterval, originalTimeout := tmuxCommand, agentReadyPollInterval, agentReadyTimeout
	t.Cleanup(func() {
		tmuxCommand, agentReadyPollInterval, agentReadyTimeout = originalCommand, originalInterval, originalTimeout
	})
	agentReadyPollInterval = 0
	agentReadyTimeout = time.Second
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
}

func TestStartTmuxTaskAcceptsClaudeTrustBeforeDeliveringPrompt(t *testing.T) {
	originalCommand, originalInterval, originalTimeout := tmuxCommand, agentReadyPollInterval, agentReadyTimeout
	t.Cleanup(func() {
		tmuxCommand, agentReadyPollInterval, agentReadyTimeout = originalCommand, originalInterval, originalTimeout
	})
	agentReadyPollInterval = 0
	agentReadyTimeout = time.Second
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
			return []byte("Claude Code v2\n❯"), nil
		}
		return nil, nil
	}
	if err := StartTmuxTask(context.Background(), t.TempDir(), "claude-ready", "claude", "message_5", "hello"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	trust := strings.Index(joined, "send-keys -t claude-ready Down Enter")
	delivery := strings.Index(joined, "load-buffer")
	if trust < 0 || delivery < 0 || trust >= delivery {
		t.Fatalf("trust was not accepted before prompt delivery: %v", calls)
	}
}
