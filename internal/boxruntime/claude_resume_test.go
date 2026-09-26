package boxruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFindClaudeResumeCandidateUsesSavedManagedWorkspace(t *testing.T) {
	base := t.TempDir()
	root, home, workspace := filepath.Join(base, ".vmbox"), filepath.Join(base, "home"), "/data/workspace"
	saved := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	if err := os.MkdirAll(filepath.Dir(TmuxSnapshotPath(root)), 0700); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(TmuxSnapshot{Version: TmuxSnapshotVersion, SavedAt: saved, Sessions: []TmuxSession{{Name: "claude-test", ManagedAgent: "claude"}}})
	if err := os.WriteFile(TmuxSnapshotPath(root), snapshot, 0600); err != nil {
		t.Fatal(err)
	}
	write := func(id, project, cwd string, at ...time.Time) string {
		t.Helper()
		dir := filepath.Join(home, ".claude", "projects", project)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		var data []byte
		for _, timestamp := range at {
			line, _ := json.Marshal(map[string]any{"type": "user", "sessionId": id, "cwd": cwd, "timestamp": timestamp})
			data = append(data, append(line, '\n')...)
		}
		path := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	oldID := "01234567-89ab-cdef-0123-456789abcdef"
	newID := "11234567-89ab-cdef-0123-456789abcdef"
	oldPath := write(oldID, "-data-workspace", workspace, saved.Add(-time.Hour), saved.Add(-5*time.Minute))
	newPath := write(newID, "-data-workspace", workspace, saved.Add(-10*time.Minute), saved.Add(-time.Minute), saved.Add(time.Minute))
	for path, at := range map[string]time.Time{oldPath: saved.Add(-5 * time.Minute), newPath: saved.Add(-time.Minute)} {
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write("21234567-89ab-cdef-0123-456789abcdef", "-data-workspace", workspace, saved.Add(time.Minute))
	write("31234567-89ab-cdef-0123-456789abcdef", "-data-other", "/data/other", saved.Add(-time.Second))
	write("41234567-89ab-cdef-0123-456789abcdef", "-data-workspace", "/data/other", saved.Add(-time.Second))
	candidate, err := FindClaudeResumeCandidate(root, home, workspace, "claude-test")
	if err != nil || candidate == nil || candidate.SessionID != newID || !candidate.LastActiveAt.Equal(saved.Add(-time.Minute)) {
		t.Fatalf("candidate = %+v, %v", candidate, err)
	}
	other, err := FindClaudeResumeCandidate(root, home, workspace, "claude-other")
	if err != nil || other != nil {
		t.Fatalf("unrelated session candidate = %+v, %v", other, err)
	}
	selected, _ := json.Marshal(TmuxSnapshot{Version: TmuxSnapshotVersion, SavedAt: saved, Sessions: []TmuxSession{{Name: "claude-test", ManagedAgent: "claude", ConversationChecked: true, ConversationID: oldID}}})
	if err := os.WriteFile(TmuxSnapshotPath(root), selected, 0600); err != nil {
		t.Fatal(err)
	}
	candidate, err = FindClaudeResumeCandidate(root, home, workspace, "claude-test")
	if err != nil || candidate == nil || candidate.SessionID != oldID {
		t.Fatalf("selected Claude conversation = %+v, %v", candidate, err)
	}
}

func TestRestoreClaudeConversationRespawnsVisiblePaneWithSavedSession(t *testing.T) {
	base := t.TempDir()
	t.Setenv("VMBOX_WORKSPACE_ROOT", base)
	root, home, workspace := filepath.Join(base, ".vmbox"), WorkloadHome(), WorkspaceDirectory()
	saved := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	id := "01234567-89ab-cdef-0123-456789abcdef"
	if err := os.MkdirAll(filepath.Dir(TmuxSnapshotPath(root)), 0700); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(TmuxSnapshot{Version: TmuxSnapshotVersion, SavedAt: saved, Sessions: []TmuxSession{{Name: "claude-test", ManagedAgent: "claude"}}})
	if err := os.WriteFile(TmuxSnapshotPath(root), snapshot, 0600); err != nil {
		t.Fatal(err)
	}
	project := "-" + strings.ReplaceAll(strings.TrimPrefix(workspace, "/"), "/", "-")
	dir := filepath.Join(home, ".claude", "projects", project)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(map[string]any{"type": "user", "sessionId": id, "cwd": workspace, "timestamp": saved.Add(-time.Minute)})
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), append(line, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	previousCommand, previousSettle, previousChannelWait := tmuxCommand, agentReadySettlePause, claudeChannelReadyWait
	t.Cleanup(func() {
		tmuxCommand, agentReadySettlePause, claudeChannelReadyWait = previousCommand, previousSettle, previousChannelWait
	})
	agentReadySettlePause = func(context.Context) error { return nil }
	channelWaited := false
	claudeChannelReadyWait = func(_ context.Context, session string, _ map[string]struct{}) error {
		channelWaited = session == "claude-test"
		return nil
	}
	var respawn []string
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "show-environment":
			return []byte(taskAgentEnvironment + "=claude\n"), nil
		case "respawn-pane":
			respawn = append([]string(nil), args...)
		case "capture-pane":
			return []byte("Claude Code v2.1.282\n❯ Try \"fix a bug\""), nil
		}
		return nil, nil
	}
	if err := RestoreClaudeConversation(context.Background(), root, "claude-test", ResumeCandidate{SessionID: id, SavedAt: saved}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(respawn, " ")
	if !strings.Contains(joined, "respawn-pane -k -t =claude-test:0.0") || !strings.Contains(joined, "--resume "+id) || !channelWaited {
		t.Fatalf("Claude resume did not replace the pane and await its channel: %q, channel waited: %t", joined, channelWaited)
	}
}
