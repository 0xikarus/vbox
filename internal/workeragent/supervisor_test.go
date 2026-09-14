package workeragent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWorkerProcessLockExcludesDuplicatesAndSymlinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.lock")
	first, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := Lock(path); err == nil {
		second.Close()
		t.Fatal("duplicate process lock accepted")
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if lock, err := Lock(link); err == nil {
		lock.Close()
		t.Fatal("symlink lock accepted")
	}
	first.Close()
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if lock, err := Lock(path); err == nil {
		lock.Close()
		t.Fatal("public lock accepted")
	}
}

func TestSupervisorRestartsOnlyAgentAndSanitizesEnvironment(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	pidFile := filepath.Join(dir, "pid")
	script := filepath.Join(dir, "agent-fixture")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
test -z "${RAILWAY_TOKEN:-}" || exit 99
if test ! -f "$1"; then
  printf first >"$1"
  exit 7
fi
printf '%s' "$$" >"$2"
exec sleep 30
`), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RAILWAY_TOKEN", "disposable-test-value")
	// This independent local process stands in for an existing workload. It is
	// explicitly disposable and is cleaned up only by this test.
	sibling := exec.Command("sleep", "30")
	if err := sibling.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { sibling.Process.Kill(); sibling.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- supervise(ctx, filepath.Join(dir, "config.json"), []string{script, marker, pidFile}, nil)
	}()
	var pid int
	for pid == 0 {
		data, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		if pid == 0 {
			select {
			case <-ctx.Done():
				t.Fatal("supervisor did not restart child")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop child")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("agent child still exists: %v", err)
	}
	if err := sibling.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("unrelated workload stopped: %v", err)
	}
}
