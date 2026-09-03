package boxruntime

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
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
