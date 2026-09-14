package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

func TestEmptyNativeSessionProbeHandlesAbsentSocketAndRejectsPermissionError(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("requires local tmux")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root := t.TempDir()
	run := func(path string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "sh", "-c", verifyEmptyNativeSessions)
		cmd.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root, "PATH="+path)
		return cmd.CombinedOutput()
	}
	if output, err := run(os.Getenv("PATH")); err != nil || strings.TrimSpace(string(output)) != emptyNativeSessionMarker {
		if !strings.Contains(string(output), "Operation not permitted") {
			t.Fatalf("real tmux absent socket was not accepted: %q %v", output, err)
		}
		t.Log("sandbox blocked the real tmux socket probe; privileged suite verifies the absent-socket result")
	}
	bin := t.TempDir()
	fake := filepath.Join(bin, "tmux")
	script := "#!/bin/sh\nprintf 'error connecting to %s/tmux-%s/default (Permission denied)\\n' \"${TMUX_TMPDIR:-/tmp}\" \"$(id -u)\" >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := run(bin + string(os.PathListSeparator) + os.Getenv("PATH")); err == nil {
		t.Fatalf("permission error was accepted: %q", output)
	}
}

func TestAttachingWorkerProvesEmptySessionsBeforeBinding(t *testing.T) {
	for _, test := range []struct {
		name       string
		probe      string
		probeErr   error
		wantCalls  int
		wantFailed bool
	}{
		{name: "empty", probe: emptyNativeSessionMarker, wantCalls: 2},
		{name: "sessions", probe: "$0\n", wantCalls: 1, wantFailed: true},
		{name: "ambiguous", probeErr: errors.New("tmux unavailable"), wantCalls: 1, wantFailed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var commands [][]string
			run := func(_ context.Context, argv []string) ([]byte, error) {
				commands = append(commands, append([]string(nil), argv...))
				if len(commands) == 1 {
					return []byte(test.probe), test.probeErr
				}
				return nil, nil
			}
			err := prepareAttachingWorker(context.Background(), strings.Repeat("a", 64), run)
			if (err != nil) != test.wantFailed {
				t.Fatalf("error=%v wantFailed=%v", err, test.wantFailed)
			}
			if len(commands) != test.wantCalls {
				t.Fatalf("commands=%v wantCalls=%d", commands, test.wantCalls)
			}
			if len(commands) > 1 && (len(commands[1]) != 3 || commands[1][1] != "native-bind") {
				t.Fatalf("second command did not bind after the proof: %v", commands)
			}
		})
	}
}

func TestMigrationSessionVerificationRealTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("requires local tmux")
	}
	// Every operation, including cleanup, is restricted to this disposable socket.
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("TMUX", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = exec.CommandContext(cleanup, "tmux", "kill-server").Run()
	})
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput(); err != nil {
			t.Fatalf("private tmux fixture: %v %s", err, out)
		}
	}
	fence := strings.Repeat("c", 64)
	if err := boxruntime.SetNativeAssignment(ctx, fence); err != nil {
		t.Fatal(err)
	}
	run("new-session", "-d", "-s", "disposable-survivor", "sleep 30")
	observe := func() v1.SessionInventory {
		t.Helper()
		inventory, err := boxruntime.NativeSessions(ctx, fence)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(inventory)
		verified, err := migrationInventory(data, fence)
		if err != nil {
			t.Fatal(err)
		}
		return verified
	}
	before := observe()
	if !survivingMigrationSessions(before, observe()) {
		t.Fatal("unchanged sessions rejected")
	}
	run("new-session", "-d", "-s", "disposable-new-session", "sleep 30")
	if !survivingMigrationSessions(before, observe()) {
		t.Fatal("additional session rejected despite original survival")
	}
	run("kill-session", "-t", before.Sessions[0].ID)
	run("new-session", "-d", "-s", "disposable-survivor", "sleep 30")
	if survivingMigrationSessions(before, observe()) {
		t.Fatal("recreated session accepted as surviving")
	}
	data, _ := json.Marshal(before)
	if _, err := migrationInventory(data, strings.Repeat("d", 64)); err == nil {
		t.Fatal("stale assignment accepted")
	}
	before.Partial = true
	data, _ = json.Marshal(before)
	if _, err := migrationInventory(data, fence); err == nil {
		t.Fatal("partial inventory accepted")
	}
}
