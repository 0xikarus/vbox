package boxruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseTmuxPanesPreservesUnicodeAndNativeAgentResume(t *testing.T) {
	separator := "\x1f"
	rows := []string{
		strings.Join([]string{"vmbox", "0", "Arbeit", "layout-a", "0", "Grüße – äöü ÄÖÜ ß €", "/data/workspace/über", "codex", "0", "1", "1"}, separator),
		strings.Join([]string{"vmbox", "0", "Arbeit", "layout-a", "1", "Shell", "/data/workspace", "bash", "0", "1", "0"}, separator),
	}
	snapshot, err := parseTmuxPanes([]byte(strings.Join(rows, "\n")+"\n"), time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sessions) != 1 || len(snapshot.Sessions[0].Windows) != 1 || len(snapshot.Sessions[0].Windows[0].Panes) != 2 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	pane := snapshot.Sessions[0].Windows[0].Panes[0]
	if pane.Title != "Grüße – äöü ÄÖÜ ß €" || pane.WorkingDirectory != "/data/workspace/über" {
		t.Fatalf("unicode changed: %+v", pane)
	}
	if !reflect.DeepEqual(pane.ResumeArgv, []string{"codex", "resume", "--last"}) || pane.ResumeStrategy != "codex-latest-in-directory" {
		t.Fatalf("resume=%q strategy=%q", pane.ResumeArgv, pane.ResumeStrategy)
	}
}

func TestAgentResumeUsesExplicitSessionIdentifiers(t *testing.T) {
	tests := []struct {
		command string
		argv    []string
		want    []string
	}{
		{"codex", []string{"codex", "resume", "0199-id"}, []string{"codex", "resume", "0199-id"}},
		{"claude", []string{"claude", "--resume=session-id"}, []string{"claude", "--resume", "session-id"}},
		{"opencode", []string{"opencode", "--session", "session-id"}, []string{"opencode", "--session", "session-id"}},
	}
	for _, test := range tests {
		got, _ := agentResume(test.command, test.argv)
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s resume=%q want=%q", test.command, got, test.want)
		}
	}
}

func TestShellResumePrintsCurrentPersistentWelcome(t *testing.T) {
	for _, command := range []string{"bash", "sh", "zsh", "fish"} {
		got, strategy := agentResume(command, []string{command})
		if !reflect.DeepEqual(got, []string{"vmbox-runtime", "welcome"}) || strategy != "shell" {
			t.Fatalf("%s resume=%q strategy=%q", command, got, strategy)
		}
	}
}

func TestOldShellSnapshotMigratesToCurrentWelcome(t *testing.T) {
	pane := TmuxPane{ResumeStrategy: "shell", ResumeArgv: []string{"/bin/bash", "-l"}, ScrollbackFile: "/data/.vmbox/tmux/shell.log"}
	command := restoredPaneCommand(TmuxSnapshot{}, pane)
	if !strings.Contains(command, "cat -- '/data/.vmbox/tmux/shell.log'") || !strings.Contains(command, "exec 'vmbox-runtime' 'welcome'") {
		t.Fatalf("old shell snapshot command=%q", command)
	}
	if strings.Contains(command, "/bin/bash") {
		t.Fatalf("old login shell bypassed current welcome: %q", command)
	}
}

func TestRestoredPaneNeverReplaysArbitraryCommand(t *testing.T) {
	saved := time.Unix(2, 0).UTC()
	pane := TmuxPane{
		CurrentCommand: "deploy-production",
		ProcessArgv:    []string{"deploy-production", "--force", "customer"},
		ScrollbackFile: "/data/.vmbox/tmux/pane.log",
	}
	command := restoredPaneCommand(TmuxSnapshot{SavedAt: saved}, pane)
	if strings.Contains(command, "deploy-production") {
		t.Fatalf("arbitrary command would be replayed: %s", command)
	}
	if !strings.Contains(command, "interrupted-pane") || !strings.Contains(command, "pane.log") {
		t.Fatalf("missing safe reconstruction: %s", command)
	}
}

func TestDecodeInterruptedPaneExplainsHonestRestoration(t *testing.T) {
	value, _ := json.Marshal(map[string]any{
		"command":   "bun test",
		"stoppedAt": time.Unix(3, 0).UTC(),
		"log":       "/data/.vmbox/tmux/pane.log",
	})
	var output bytes.Buffer
	if err := DecodeInterruptedPane(base64.RawURLEncoding.EncodeToString(value), &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, required := range []string{"did not remain alive", "bun test", "Saved scrollback", "never re-runs arbitrary commands"} {
		if !strings.Contains(text, required) {
			t.Fatalf("output missing %q: %s", required, text)
		}
	}
}

func TestParseTmuxPanesHandlesInterleavedSessionsAndWindows(t *testing.T) {
	separator := "\x1f"
	row := func(session, window, pane string) string {
		return strings.Join([]string{session, window, "window-" + window, "layout", pane, "pane-" + pane, "/data/workspace", "bash", "0", "1", "1"}, separator)
	}
	rows := []string{row("one", "0", "0"), row("two", "0", "0"), row("one", "1", "0"), row("one", "0", "1"), row("two", "1", "0")}
	snapshot, err := parseTmuxPanes([]byte(strings.Join(rows, "\n")+"\n"), time.Unix(4, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sessions) != 2 || len(snapshot.Sessions[0].Windows) != 2 || len(snapshot.Sessions[1].Windows) != 2 {
		t.Fatalf("interleaved snapshot shape=%+v", snapshot)
	}
	if got := len(snapshot.Sessions[0].Windows[0].Panes); got != 2 {
		t.Fatalf("first window panes=%d, want 2", got)
	}
	if snapshot.Sessions[0].Windows[0].Panes[1].Title != "pane-1" {
		t.Fatalf("pane was appended through a stale slice pointer: %+v", snapshot.Sessions[0].Windows[0])
	}
}

func TestTmuxServerAbsentRecognizesMissingSocketOnly(t *testing.T) {
	for _, message := range []string{
		"no server running on /tmp/tmux-10001/default",
		"failed to connect to server",
		"no sessions",
		"error connecting to /tmp/tmux-10001/default (No such file or directory)",
	} {
		if !tmuxServerAbsent(errors.New(message)) {
			t.Errorf("did not recognize absent tmux server: %s", message)
		}
	}
	if tmuxServerAbsent(errors.New("error connecting to /tmp/tmux-10001/default (Permission denied)")) {
		t.Fatal("permission failure was mistaken for an absent tmux server")
	}
}

func TestTmuxContextPersistsBeforeAnySessionExists(t *testing.T) {
	bin := t.TempDir()
	tmux := filepath.Join(bin, "tmux")
	if err := os.WriteFile(tmux, []byte("#!/bin/sh\necho 'no server running' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	root := t.TempDir()
	if err := SetTmuxContext(context.Background(), root, "research", "fleet-slot-2", "running", "connected"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(tmuxContextPath(root))
	if err != nil {
		t.Fatal(err)
	}
	var value TmuxContext
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if value != (TmuxContext{Box: "research", Slot: "fleet-slot-2", State: "running", Health: "connected"}) {
		t.Fatalf("context=%+v", value)
	}
	info, err := os.Stat(tmuxContextPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("context mode=%v", info.Mode().Perm())
	}
}

func TestAncestorPIDsAlwaysProtectContainerInit(t *testing.T) {
	if !ancestorPIDs()[1] {
		t.Fatal("container PID 1 was not protected from workspace process cleanup")
	}
}

func TestInfrastructureWorkspaceProcessOnlyProtectsIdleRuntime(t *testing.T) {
	for _, argv := range [][]string{{"vmbox-runtime", "idle"}, {"/usr/local/bin/vmbox-runtime", "idle"}} {
		if !infrastructureWorkspaceProcess(argv) {
			t.Fatalf("idle runtime was not protected: %q", argv)
		}
	}
	for _, argv := range [][]string{{"vmbox-runtime", "health"}, {"bash", "idle"}, {"vmbox-runtime", "idle", "extra"}, nil} {
		if infrastructureWorkspaceProcess(argv) {
			t.Fatalf("workload was incorrectly protected: %q", argv)
		}
	}
}
