package boxruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFindOpenCodeResumeCandidateUsesVisibleSnapshotAndSavedDatabase(t *testing.T) {
	base := t.TempDir()
	t.Setenv("VMBOX_WORKSPACE_ROOT", base)
	root, home, workspace := filepath.Join(base, ".vmbox"), WorkloadHome(), WorkspaceDirectory()
	saved := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	writeSnapshot := func(checked bool, id string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(TmuxSnapshotPath(root)), 0700); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(TmuxSnapshot{Version: TmuxSnapshotVersion, SavedAt: saved, Sessions: []TmuxSession{{Name: "opencode-test", ManagedAgent: "opencode", ConversationChecked: checked, ConversationID: id}}})
		if err := os.WriteFile(TmuxSnapshotPath(root), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	db := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(db), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", "-c", `import sqlite3,sys
db=sqlite3.connect(sys.argv[1]); db.execute('CREATE TABLE session(id TEXT, directory TEXT, parent_id TEXT, time_created INTEGER, time_updated INTEGER)'); db.execute('CREATE TABLE message(id TEXT, session_id TEXT, time_created INTEGER)')
s=int(sys.argv[2]); w=sys.argv[3]; rows=[('ses_visible',w,None,s-60000,s-30000),('ses_other',w,None,s-50000,s-1000),('ses_other_project','/data/other',None,s-10000,s-1),('ses_after_wake',w,None,s+1000,s+1000),('ses_child',w,'ses_visible',s-10000,s-1)]
db.executemany('INSERT INTO session VALUES(?,?,?,?,?)',rows); db.executemany('INSERT INTO message VALUES(?,?,?)',[('m1','ses_visible',s-30000),('m2','ses_other',s-1000),('m3','ses_other_project',s-1),('m4','ses_after_wake',s+1000),('m5','ses_child',s-1)]); db.commit()`, db, fmt.Sprint(saved.UnixMilli()), workspace)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create OpenCode fixture: %v: %s", err, output)
	}
	writeSnapshot(true, "ses_visible")
	candidate, err := FindOpenCodeResumeCandidate(root, home, workspace, "opencode-test")
	if err != nil || candidate == nil || candidate.SessionID != "ses_visible" {
		t.Fatalf("selected candidate = %+v, %v", candidate, err)
	}
	writeSnapshot(true, "")
	candidate, err = FindOpenCodeResumeCandidate(root, home, workspace, "opencode-test")
	if err != nil || candidate != nil {
		t.Fatalf("fresh visible TUI candidate = %+v, %v", candidate, err)
	}
	writeSnapshot(false, "")
	candidate, err = FindOpenCodeResumeCandidate(root, home, workspace, "opencode-test")
	if err != nil || candidate == nil || candidate.SessionID != "ses_other" {
		t.Fatalf("legacy snapshot candidate = %+v, %v", candidate, err)
	}
	writeSnapshot(true, "ses_visible")
	previousCommand, previousReady, previousClient, previousHealth := tmuxCommand, openCodeReadyProbe, openCodeVisibleClient, openCodeBridgeHealth
	t.Cleanup(func() {
		tmuxCommand, openCodeReadyProbe, openCodeVisibleClient, openCodeBridgeHealth = previousCommand, previousReady, previousClient, previousHealth
	})
	var respawn []string
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "show-environment" {
			return []byte(taskAgentEnvironment + "=opencode\n"), nil
		}
		if args[0] == "respawn-pane" {
			respawn = append([]string(nil), args...)
		}
		return nil, nil
	}
	openCodeReadyProbe = func(context.Context, string) (bool, error) { return true, nil }
	openCodeVisibleClient = func(context.Context, string, string) (*http.Client, error) { return &http.Client{}, nil }
	openCodeBridgeHealth = func(context.Context, *http.Client, string) (openCodeBridgeIdentity, error) {
		return openCodeBridgeIdentity{SessionID: "ses_visible"}, nil
	}
	if err := RestoreOpenCodeConversation(context.Background(), root, "opencode-test", ResumeCandidate{SessionID: "ses_visible", SavedAt: saved}); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(respawn, " "); !strings.Contains(joined, "respawn-pane -k -t =opencode-test:0.0") || !strings.Contains(joined, "--session ses_visible") {
		t.Fatalf("saved OpenCode conversation was not relaunched in the visible pane: %q", joined)
	}
}
