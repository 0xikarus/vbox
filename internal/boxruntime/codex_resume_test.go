package boxruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindCodexResumeCandidateUsesPreHibernateInteractiveThread(t *testing.T) {
	base := t.TempDir()
	root, home := filepath.Join(base, ".vmbox"), filepath.Join(base, "home")
	saved := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	if err := os.MkdirAll(filepath.Dir(TmuxSnapshotPath(root)), 0700); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(TmuxSnapshot{Version: TmuxSnapshotVersion, SavedAt: saved, Sessions: []TmuxSession{{Name: "codex-test", ManagedAgent: "codex"}}})
	if err := os.WriteFile(TmuxSnapshotPath(root), snapshot, 0600); err != nil {
		t.Fatal(err)
	}
	write := func(id, source string, at time.Time) {
		t.Helper()
		dir := filepath.Join(home, ".codex", "sessions", "2026", "09", "23")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": id, "timestamp": at, "source": source}})
		if err := os.WriteFile(filepath.Join(dir, "rollout-2026-09-23T11-00-00-"+id+".jsonl"), append(meta, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	oldID := "01234567-89ab-cdef-0123-456789abcdef"
	newID := "11234567-89ab-cdef-0123-456789abcdef"
	postWakeID := "21234567-89ab-cdef-0123-456789abcdef"
	write(oldID, "cli", saved.Add(-time.Hour))
	write(newID, "cli", saved.Add(-time.Minute))
	write(postWakeID, "cli", saved.Add(time.Minute))
	write("31234567-89ab-cdef-0123-456789abcdef", "exec", saved.Add(-time.Second))
	candidate, err := FindCodexResumeCandidate(root, home, "codex-test")
	if err != nil || candidate == nil || candidate.SessionID != newID {
		t.Fatalf("candidate = %+v, %v", candidate, err)
	}
	other, err := FindCodexResumeCandidate(root, home, "codex-other")
	if err != nil || other != nil {
		t.Fatalf("unrelated session candidate = %+v, %v", other, err)
	}
}
