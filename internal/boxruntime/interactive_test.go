package boxruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInteractiveShellUsesSpecsWelcome(t *testing.T) {
	port := fmt.Sprintf("%d", OpenCodeChatPort("managed-session"))
	for agent, want := range map[string][]string{
		"shell":    {"vmbox-runtime", "welcome"},
		"codex":    {"codex", "--remote", codexTUIProxyURL("managed-session"), "-c", "check_for_update_on_startup=false", "-c", "suppress_unstable_features_warning=true", "-c", "notice.hide_rate_limit_model_nudge=true"},
		"claude":   {"env", "DISABLE_AUTOUPDATER=1", "claude", "--add-dir", "/data/home/.local/share/vmbox/chat", "--dangerously-load-development-channels", "server:vmbox-desktop"},
		"opencode": {"opencode", "--auto", "--hostname", "127.0.0.1", "--port", port},
	} {
		got, err := interactiveArgv("managed-session", agent)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %v %v", agent, got, err)
		}
	}
	if _, err := interactiveArgv("managed-session", "invalid"); err == nil {
		t.Fatal("invalid agent accepted")
	}
}

func TestRecoverInterruptedCodexSessionLeavesUserShellAlone(t *testing.T) {
	original := tmuxCommand
	t.Cleanup(func() { tmuxCommand = original })
	var calls []string
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, args[0])
		switch args[0] {
		case "display-message":
			return []byte("bash\n"), nil
		case "capture-pane":
			return []byte("ordinary user shell\n"), nil
		default:
			t.Fatalf("unexpected tmux command: %v", args)
			return nil, nil
		}
	}
	if err := recoverInterruptedCodexSession(context.Background(), "/data/.vmbox", "codex-test"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"display-message", "capture-pane"}) {
		t.Fatalf("calls=%v", calls)
	}
}

func TestRecoverInterruptedCodexSessionRestartsManagedPane(t *testing.T) {
	original := tmuxCommand
	t.Cleanup(func() { tmuxCommand = original })
	var respawn []string
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "display-message":
			return []byte("bash\n"), nil
		case "capture-pane":
			return []byte("[vmbox] Interrupted: codex --remote\n"), nil
		case "respawn-pane":
			respawn = append([]string(nil), args...)
			return nil, nil
		default:
			t.Fatalf("unexpected tmux command: %v", args)
			return nil, nil
		}
	}
	if err := recoverInterruptedCodexSession(context.Background(), "/data/.vmbox", "codex-test"); err != nil {
		t.Fatal(err)
	}
	if len(respawn) == 0 || !strings.Contains(strings.Join(respawn, " "), "agent-restore") {
		t.Fatalf("managed pane was not restarted: %v", respawn)
	}
}

func TestRecoverCodexMCPStartupDoesNotRestartWithoutCurrentPolicy(t *testing.T) {
	originalCommand, originalPolicy := tmuxCommand, codexDesktopMCPPolicy
	t.Cleanup(func() { tmuxCommand, codexDesktopMCPPolicy = originalCommand, originalPolicy })
	var calls []string
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, args[0])
		switch args[0] {
		case "capture-pane":
			return []byte("MCP startup issue · Ask Codex to do anything"), nil
		case "show-option":
			return []byte("fence\n"), nil
		default:
			t.Fatalf("unexpected process restart before policy refresh: %v", args)
			return nil, nil
		}
	}
	codexDesktopMCPPolicy = func(context.Context, string) (map[string]bool, error) {
		return nil, fmt.Errorf("stale assignment")
	}
	if err := recoverCodexMCPStartup(context.Background(), "codex-test"); err == nil || !strings.Contains(err.Error(), "policy unavailable") {
		t.Fatalf("missing current policy was accepted: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"capture-pane", "show-option"}) {
		t.Fatalf("commands after policy failure: %v", calls)
	}
}

func TestInteractiveAppliesPreviouslySavedContext(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TMUX_TEST_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("TMUX_TEST_LOG", log)
	root := t.TempDir()
	if err := SetTmuxContext(context.Background(), root, "fresh-box", "slot-2", "running", "connected"); err != nil {
		t.Fatal(err)
	}
	if err := StartInteractive(context.Background(), root, "new-shell", "shell"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"VMBOX_NAME fresh-box", "VMBOX_COMPUTE_SLOT slot-2", "VMBOX_ASSIGNMENT_STATE running", "VMBOX_CONNECTION_HEALTH connected"} {
		if !strings.Contains(string(data), "set-environment -t new-shell "+value) {
			t.Errorf("context not applied: %s", value)
		}
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
	t.Cleanup(func() {
		_, _ = tmuxOutput(context.Background(), "set-option", "-g", "exit-empty", "on")
		_, _ = tmuxOutput(context.Background(), "kill-session", "-t", "=specs-test")
	})
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
	if err := SetNativeAssignment(ctx, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := tmuxOutput(ctx, "kill-session", "-t", "=specs-test"); err != nil {
		t.Fatal(err)
	}
	empty, err := SaveTmuxState(ctx, root)
	if err != nil || len(empty.Sessions) != 0 {
		t.Fatalf("empty bound server snapshot: %+v %v", empty, err)
	}
}
