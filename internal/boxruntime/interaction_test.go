package boxruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
