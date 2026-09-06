package boxruntime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestClipboardRecorderHelper(t *testing.T) {
	if os.Getenv("VMBOX_TEST_RECORDER") == "" {
		return
	}
	fmt.Print("\x1b[?2004hclipboard-ready")
	TestNativeRecorderHelper(t)
}

// Real isolated tmux: select screen text, inspect its buffer independently,
// then paste Unicode/multiline data into a raw input recorder.
func TestManagedTmuxCopyPaste(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	conf, err := filepath.Abs("../../tmux.conf")
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, "tmux", append([]string{"-S", socket, "-f", conf}, args...)...)
		cmd.Env = append(os.Environ(), "TMUX=", "TERM=xterm-256color")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
		return out
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	marker := "copy-" + ID("")
	run("new-session", "-d", "-s", "source", "printf '%s\\n' '"+marker+"'; sleep 30")
	for key, value := range map[string]string{"VMBOX_NAME": "fresh-box", "VMBOX_COMPUTE_SLOT": "slot-2", "VMBOX_ASSIGNMENT_STATE": "running", "VMBOX_CONNECTION_HEALTH": "connected"} {
		run("set-environment", "-t", "source", key, value)
	}
	footer := strings.TrimSpace(string(run("show-options", "-g", "-v", "status-format[0]")))
	for _, width := range []string{"80", "180"} {
		format := strings.ReplaceAll(footer, "#{client_width}", width)
		rendered := string(run("display-message", "-p", "-t", "source", format))
		plain := regexp.MustCompile(`#\[[^\]]*\]`).ReplaceAllString(rendered, "")
		for _, want := range []string{"fresh-box", "slot-2"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("footer width %s missing %s: %s", width, want, plain)
			}
		}
		if strings.Contains(plain, "nobold]") {
			t.Fatalf("malformed footer: %s", plain)
		}
		if width == "180" && (!strings.Contains(plain, "STATE: running") || !strings.Contains(plain, "NET: connected")) {
			t.Fatalf("missing wide footer details: %s", plain)
		}
	}
	wait := func(check func() bool) {
		t.Helper()
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
			if check() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("tmux observation timed out")
	}
	wait(func() bool { return strings.Contains(string(run("capture-pane", "-p", "-t", "source")), marker) })
	run("copy-mode", "-t", "source")
	for _, action := range []string{"history-top", "start-of-line", "begin-selection", "end-of-line", "copy-selection-and-cancel"} {
		run("send-keys", "-t", "source", "-X", action)
	}
	if got := string(run("save-buffer", "-")); got != marker {
		t.Fatalf("copy mismatch: %q", got)
	}
	for _, key := range []string{"Enter", "MouseDragEnd1Pane"} {
		if !strings.Contains(string(run("list-keys", "-T", "copy-mode", key)), "copy-selection-and-cancel") {
			t.Fatal("missing copy binding", key)
		}
	}
	if !strings.Contains(string(run("list-keys", "-T", "prefix", "v")), "paste-buffer -p") {
		t.Fatal("missing safe paste binding")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	received := filepath.Join(t.TempDir(), "received")
	run("new-session", "-d", "-s", "sink", fmt.Sprintf("env VMBOX_TEST_RECORDER=%q %q -test.run=^TestClipboardRecorderHelper$", received, exe))
	wait(func() bool { _, err := os.Stat(received + ".ready"); return err == nil })
	wait(func() bool {
		return strings.Contains(string(run("capture-pane", "-p", "-t", "sink")), "clipboard-ready")
	})
	payload := marker + "-äöüß€\nsecond-line"
	run("set-buffer", "--", payload)
	run("paste-buffer", "-p", "-t", "sink")
	want := []byte("\x1b[200~" + strings.ReplaceAll(payload, "\n", "\r") + "\x1b[201~")
	wait(func() bool { data, _ := os.ReadFile(received); return len(data) >= len(want) })
	got, err := os.ReadFile(received)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("paste mismatch: %x want %x (%v)", got, want, err)
	}
}
