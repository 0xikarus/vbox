package boxruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestOneShotAppliesSavedTmuxContext(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TMUX_TEST_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("TMUX_TEST_LOG", log)
	root := t.TempDir()
	ctx := context.Background()
	if err := SetTmuxContext(ctx, root, "once-box", "slot-3", "running", "connected"); err != nil {
		t.Fatal(err)
	}
	if err := StartProcess(ctx, root, v1.ProcessTask{ID: "footer-test", Session: "task-footer", Agent: "shell", Prompt: "true"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"VMBOX_NAME once-box", "VMBOX_COMPUTE_SLOT slot-3", "VMBOX_ASSIGNMENT_STATE running", "VMBOX_CONNECTION_HEALTH connected"} {
		if !strings.Contains(string(b), "set-environment -t task-footer "+want) {
			t.Fatalf("one-shot missing %s", want)
		}
	}
}

func TestTmuxFooterUpgradesOldWorkerConfiguration(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	bin := t.TempDir()
	// Simulate an old image: sourcing its config leaves the broken footer.
	wrapper := "#!/bin/sh\nif [ \"$1\" = source-file ]; then exit 0; fi\nexec " + shellQuote(realTmux) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("TMUX", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Cleanup(func() { _, _ = tmuxOutput(context.Background(), "kill-server") })
	run := func(args ...string) string {
		t.Helper()
		out, err := tmuxOutput(ctx, args...)
		if err != nil {
			t.Fatalf("%v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	run("new-session", "-d", "-s", "task-footer", "sleep 30")
	old := strings.ReplaceAll(tmuxFooter, "#[align=right]#[nobold]", "#[align=right,nobold]")
	old = strings.ReplaceAll(old, "#[align=left]#[bold]", "#[align=left,bold]")
	run("set-option", "-g", "status-format[0]", old)
	plain := func(format string) string {
		return regexp.MustCompile(`#\[[^\]]*\]`).ReplaceAllString(run("display-message", "-p", "-t", "task-footer", format), "")
	}
	if !strings.Contains(plain(strings.ReplaceAll(old, "#{client_width}", "180")), "nobold]") {
		t.Fatal("old configuration did not reproduce the reported defect")
	}
	if err := SetTmuxContext(ctx, t.TempDir(), "once-box", "slot-3", "running", "connected"); err != nil {
		t.Fatal(err)
	}
	footer := run("show-options", "-gv", "status-format[0]")
	for _, width := range []string{"80", "180"} {
		got := plain(strings.ReplaceAll(footer, "#{client_width}", width))
		for _, want := range []string{"once-box", "slot-3", "task-footer"} {
			if !strings.Contains(got, want) {
				t.Fatalf("width %s missing %s: %s", width, want, got)
			}
		}
		if strings.Contains(got, "nobold]") || (width == "180" && (!strings.Contains(got, "STATE: running") || !strings.Contains(got, "NET: connected"))) {
			t.Fatalf("bad footer at width %s: %s", width, got)
		}
	}
}
