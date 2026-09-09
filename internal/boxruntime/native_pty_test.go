package boxruntime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"
)

func TestNativeRecorderHelper(t *testing.T) {
	path := os.Getenv("VMBOX_TEST_RECORDER")
	if path == "" {
		return
	}
	state, err := term.MakeRaw(0)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Restore(0, state)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = os.WriteFile(path+".ready", []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(f, os.Stdin)
}

func TestNativeAttachHelper(t *testing.T) {
	if os.Getenv("VMBOX_TEST_ATTACH") == "" {
		return
	}
	if err := NativeAttach(context.Background(), t.TempDir(), os.Getenv("VMBOX_TEST_FENCE"), os.Getenv("VMBOX_TEST_SESSION"), os.Getenv("VMBOX_TEST_INCARNATION")); err != nil {
		t.Fatal(err)
	}
}

func TestNativeInputPTY(t *testing.T) {
	for _, bin := range []string{"tmux", "script"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " unavailable")
		}
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("TMUX", "")
	t.Setenv("TERM", "xterm-256color")
	fence := strings.Repeat("c", 64)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Cleanup(func() { _, _ = tmuxOutput(context.Background(), "kill-server") })
	if err := SetNativeAssignment(ctx, fence); err != nil {
		t.Fatal(err)
	}
	if _, err := tmuxOutput(ctx, "set-option", "-g", "prefix", "C-a"); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	recorded := filepath.Join(t.TempDir(), "received.bytes")
	command := fmt.Sprintf("env VMBOX_TEST_RECORDER=%q %q -test.run='^TestNativeRecorderHelper$'", recorded, exe)
	id, err := tmuxOutput(ctx, "new-session", "-d", "-P", "-F", "#{session_id}", "-s", "input", command)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if check() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("bounded PTY observation timed out")
	}
	wait(func() bool { _, err := os.Stat(recorded + ".ready"); return err == nil })
	// util-linux script provides an actual client PTY; input does not use
	// send-keys or a mocked transport. The independent raw program records bytes.
	attach := exec.CommandContext(ctx, "script", "-q", "-E", "never", "-c", fmt.Sprintf("%q -test.run='^TestNativeAttachHelper$'", exe), "/dev/null")
	inv, err := NativeSessions(ctx, fence)
	if err != nil || len(inv.Sessions) != 1 {
		t.Fatal("session observation", err)
	}
	attach.Env = append(os.Environ(), "VMBOX_TEST_ATTACH=1", "VMBOX_TEST_FENCE="+fence, "VMBOX_TEST_SESSION="+strings.TrimSpace(string(id)), "VMBOX_TEST_INCARNATION="+inv.Sessions[0].Incarnation)
	input, err := attach.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	attach.Stdout = io.Discard
	attach.Stderr = io.Discard
	if err = attach.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		input.Close()
		if attach.ProcessState == nil {
			_ = attach.Process.Kill()
			_ = attach.Wait()
		}
	})
	wait(func() bool {
		out, _ := tmuxOutput(ctx, "list-clients", "-F", "#{client_session}")
		return strings.Contains(string(out), "input")
	})
	want := []byte("unique-" + ID("") + "-QWERTZ-äöüß€\rline1\rline2\x7f\x1b[A\x1b[B\x1b[C\x1b[D\x03\x04\x1a")
	if _, err = input.Write(want); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { data, _ := os.ReadFile(recorded); return len(data) >= len(want) })
	got, _ := os.ReadFile(recorded)
	if !bytes.Equal(got, want) {
		t.Fatalf("raw input mismatch: intended=%x received=%x", want, got)
	}
	// Detach is consumed by tmux, not delivered to the program.
	_, _ = input.Write([]byte{1, 'd'})
	_ = input.Close()
	if err = attach.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err = tmuxOutput(ctx, "has-session", "-t", strings.TrimSpace(string(id))); err != nil {
		t.Fatal("detach killed recorder", err)
	}
	after, _ := os.ReadFile(recorded)
	if !bytes.Equal(after, want) {
		t.Fatal("detach replayed or added input")
	}
	reconnected := exec.CommandContext(ctx, attach.Args[0], attach.Args[1:]...)
	reconnected.Env = attach.Env
	reconnected.Stdout = io.Discard
	reconnected.Stderr = io.Discard
	pipe, err := reconnected.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = reconnected.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pipe.Close()
		if reconnected.ProcessState == nil {
			_ = reconnected.Process.Kill()
			_ = reconnected.Wait()
		}
	})
	wait(func() bool {
		out, _ := tmuxOutput(ctx, "list-clients", "-F", "#{client_session}")
		return strings.Contains(string(out), "input")
	})
	_, _ = pipe.Write([]byte{1, 'd'})
	pipe.Close()
	if err = reconnected.Wait(); err != nil {
		t.Fatal(err)
	}
	after, _ = os.ReadFile(recorded)
	if !bytes.Equal(after, want) {
		t.Fatal("reconnect replayed input")
	}
}
