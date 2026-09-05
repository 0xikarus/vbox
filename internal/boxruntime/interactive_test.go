package boxruntime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInteractiveShellUsesSpecsWelcome(t *testing.T) {
	for _, agent := range []string{"shell", "codex", "claude"} {
		want := []string{agent}
		if agent == "shell" {
			want = []string{"vmbox-runtime", "welcome"}
		}
		got, err := interactiveArgv(agent)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %v %v", agent, got, err)
		}
	}
	if _, err := interactiveArgv("invalid"); err == nil {
		t.Fatal("invalid agent accepted")
	}
}

func TestInteractiveShellRealWelcome(t *testing.T) {
	testInteractiveShellReal(t, false)
}

func TestInteractiveStartCLIRealShell(t *testing.T) {
	testInteractiveShellReal(t, true)
}

func testInteractiveShellReal(t *testing.T, startCLI bool) {
	runtime := os.Getenv("VMBOX_TEST_RUNTIME_BINARY")
	if runtime == "" {
		t.Skip("requires built runtime and native tmux")
	}
	base := t.TempDir()
	for _, dir := range []string{"bin", "workspace", "home", "socket"} {
		if err := os.Mkdir(filepath.Join(base, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(runtime, filepath.Join(base, "bin", "vmbox-runtime")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(base, "bin")+":"+os.Getenv("PATH"))
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("TMUX_TMPDIR", filepath.Join(base, "socket"))
	t.Setenv("TMUX", "")
	banner := "Specs: 2 CPU / 4096 MiB RAM / 10 GiB disk\n"
	if err := os.WriteFile(filepath.Join(base, "home", ".vmbox-welcome"), []byte(banner), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.Cleanup(func() { _, _ = tmuxOutput(context.Background(), "kill-session", "-t", "=specs-test") })
	command := ""
	marker := filepath.Join(base, "startup-count")
	if startCLI {
		command = "printf 'started\\n' >> " + shellQuote(marker) + "; exit 7"
	}
	if err := StartInteractiveCommand(ctx, filepath.Join(base, ".vmbox"), "specs-test", "shell", command); err != nil {
		t.Fatal(err)
	}
	for {
		out, err := tmuxOutput(ctx, "capture-pane", "-p", "-t", "=specs-test:0.0")
		if err == nil && strings.Contains(string(out), strings.TrimSpace(banner)) {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("welcome not visible: %s %v", out, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := tmuxOutput(ctx, "send-keys", "-t", "=specs-test:0.0", "printf 'shell-%s-ready\\n' functional", "Enter"); err != nil {
		t.Fatal(err)
	}
	for {
		out, err := tmuxOutput(ctx, "capture-pane", "-p", "-t", "=specs-test:0.0")
		if err == nil && strings.Contains(string(out), "shell-functional-ready") {
			if startCLI {
				data, err := os.ReadFile(marker)
				if err != nil || string(data) != "started\n" {
					t.Fatalf("startup replayed or not executed: %q %v", data, err)
				}
				if !strings.Contains(string(out), "exit status 7") {
					t.Fatal("startup failure was hidden")
				}
			}
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("shell not functional: %s %v", out, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !startCLI {
		return
	}
	root := filepath.Join(base, ".vmbox")
	before, err := tmuxOutput(ctx, "display-message", "-p", "-t", "=specs-test:0.0", "#{pane_pid}")
	if err != nil {
		t.Fatal(err)
	}
	// A restore request against live compute must not replace the shell.
	live, err := RestoreTmuxState(ctx, root)
	if err != nil || !live.Live {
		t.Fatalf("live restore: %+v %v", live, err)
	}
	after, err := tmuxOutput(ctx, "display-message", "-p", "-t", "=specs-test:0.0", "#{pane_pid}")
	if err != nil || string(before) != string(after) {
		t.Fatalf("live shell replaced: %q -> %q %v", before, after, err)
	}
	snapshot, err := SaveTmuxState(ctx, root)
	if err != nil || len(snapshot.Sessions) != 1 || !snapshot.Sessions[0].ShellFirst {
		t.Fatalf("shell-first snapshot: %+v %v", snapshot, err)
	}
	for _, window := range snapshot.Sessions[0].Windows {
		for _, pane := range window.Panes {
			if pane.ResumeStrategy != "shell" {
				t.Fatalf("unexpected resume strategy: %s", pane.ResumeStrategy)
			}
		}
	}
	if _, err := tmuxOutput(ctx, "kill-session", "-t", "=specs-test"); err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreTmuxState(ctx, root)
	if err != nil || restored.Restored != 1 {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	if _, err := tmuxOutput(ctx, "send-keys", "-t", "=specs-test:0.0", "printf 'restored-%s-ready\\n' functional", "Enter"); err != nil {
		t.Fatal(err)
	}
	for {
		out, err := tmuxOutput(ctx, "capture-pane", "-p", "-t", "=specs-test:0.0")
		if err == nil && strings.Contains(string(out), "restored-functional-ready") {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("restored shell not functional: %s %v", out, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "started\n" {
		t.Fatalf("startup replayed after restore: %q %v", data, err)
	}
	option, err := tmuxOutput(ctx, "show-options", "-v", "-t", "=specs-test:", "@vmbox-shell")
	if err != nil || strings.TrimSpace(string(option)) != "1" {
		t.Fatalf("restored shell marker: %q %v", option, err)
	}
}
