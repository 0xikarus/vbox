package boxruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestVisibleManagedConversationUsesCurrentTUI(t *testing.T) {
	oldCodex, oldClient, oldHealth := codexVisibleThreadState, openCodeVisibleClient, openCodeBridgeHealth
	t.Cleanup(func() {
		codexVisibleThreadState, openCodeVisibleClient, openCodeBridgeHealth = oldCodex, oldClient, oldHealth
	})
	codexVisibleThreadState = func(context.Context, string) (bool, string, error) { return true, "codex-current", nil }
	id, checked := visibleManagedConversation(context.Background(), "codex", "codex-test")
	if !checked || id != "codex-current" {
		t.Fatalf("Codex visible conversation = %q checked=%t", id, checked)
	}
	openCodeVisibleClient = func(context.Context, string, string) (*http.Client, error) { return &http.Client{}, nil }
	openCodeBridgeHealth = func(context.Context, *http.Client, string) (openCodeBridgeIdentity, error) {
		return openCodeBridgeIdentity{SessionID: "ses_current"}, nil
	}
	id, checked = visibleManagedConversation(context.Background(), "opencode", "opencode-test")
	if !checked || id != "ses_current" {
		t.Fatalf("OpenCode visible conversation = %q checked=%t", id, checked)
	}
	openCodeBridgeHealth = func(context.Context, *http.Client, string) (openCodeBridgeIdentity, error) {
		return openCodeBridgeIdentity{}, nil
	}
	id, checked = visibleManagedConversation(context.Background(), "opencode", "opencode-test")
	if !checked || id != "" {
		t.Fatalf("OpenCode home view = %q checked=%t", id, checked)
	}
	base := t.TempDir()
	t.Setenv("VMBOX_WORKSPACE_ROOT", base)
	ready := claudeChannelReadyDir(WorkloadHome())
	if err := os.MkdirAll(ready, 0700); err != nil {
		t.Fatal(err)
	}
	marker, _ := json.Marshal(map[string]any{"owner": "channel_test", "pid": os.Getpid(), "sessionId": "01234567-89ab-cdef-0123-456789abcdef"})
	if err := os.WriteFile(filepath.Join(ready, "claude-test.channel_test"), marker, 0600); err != nil {
		t.Fatal(err)
	}
	id, checked = visibleManagedConversation(context.Background(), "claude", "claude-test")
	if !checked || id != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatalf("Claude channel conversation = %q checked=%t", id, checked)
	}
}
