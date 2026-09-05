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
	t.Cleanup(func() { _, _ = tmuxOutput(context.Background(), "kill-server") })
	if err := StartInteractive(ctx, filepath.Join(base, ".vmbox"), "specs-test", "shell"); err != nil {
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
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("shell not functional: %s %v", out, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
