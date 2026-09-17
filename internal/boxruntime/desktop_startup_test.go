package boxruntime

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A display lock left behind by a crashed X server must not keep the desktop
// down forever, while a lock held by a live server must be respected.
func TestClearStaleDisplayLockRemovesOnlyDeadOwners(t *testing.T) {
	lockFor := func(display string) string {
		return filepath.Join("/tmp", ".X"+strings.TrimPrefix(display, ":")+"-lock")
	}

	// A PID that cannot be running: pick one far above the current process and
	// confirm it is absent before relying on it.
	dead := os.Getpid() + 1_000_000
	display := ":91"
	lock := lockFor(display)
	if _, err := os.Stat(lock); err == nil {
		t.Skipf("%s already exists on this host", lock)
	}
	if err := os.WriteFile(lock, []byte(strconv.Itoa(dead)+"\n"), 0o644); err != nil {
		t.Skipf("cannot write %s: %v", lock, err)
	}
	defer os.Remove(lock)
	if err := clearStaleDisplayLock(display); err != nil {
		t.Fatalf("clear stale lock: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("stale display lock survived: %v", err)
	}

	liveDisplay := ":92"
	liveLock := lockFor(liveDisplay)
	if _, err := os.Stat(liveLock); err == nil {
		t.Skipf("%s already exists on this host", liveLock)
	}
	if err := os.WriteFile(liveLock, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Skipf("cannot write %s: %v", liveLock, err)
	}
	defer os.Remove(liveLock)
	if err := clearStaleDisplayLock(liveDisplay); err != nil {
		t.Fatalf("clear live lock: %v", err)
	}
	if _, err := os.Stat(liveLock); err != nil {
		t.Fatalf("lock held by a live process was removed: %v", err)
	}
}

func TestClearStaleDisplayLockIgnoresUnusableInput(t *testing.T) {
	for _, display := range []string{"", ":", ":../escape", ":9/9"} {
		if err := clearStaleDisplayLock(display); err != nil {
			t.Fatalf("display %q: %v", display, err)
		}
	}
}

// Viewer windows are keyed per session so a repeated desktop start reuses the
// window it already created instead of stacking new ones.
func TestDesktopViewerWindowNameIsStablePerSession(t *testing.T) {
	if got := desktopViewerWindowName("$3"); got != "viewer-3" {
		t.Fatalf("window name: %q", got)
	}
	if desktopViewerWindowName("$3") != desktopViewerWindowName("$3") {
		t.Fatal("window name is not stable")
	}
	if desktopViewerWindowName("$3") == desktopViewerWindowName("$4") {
		t.Fatal("distinct sessions share a window name")
	}
}

func TestLockedBufferTailReportsLastOutput(t *testing.T) {
	var buf lockedBuffer
	if got := buf.tail(100); got != "no diagnostics from Xtigervnc" {
		t.Fatalf("empty tail: %q", got)
	}
	buf.Write([]byte("Fatal server error:\nServer is already active for display 99\n"))
	got := buf.tail(200)
	if !strings.Contains(got, "already active for display 99") {
		t.Fatalf("tail lost the reason: %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("tail must be a single line: %q", got)
	}
	buf.Write([]byte(strings.Repeat("x", 500)))
	if len(buf.tail(100)) > 104 {
		t.Fatalf("tail exceeded its limit: %d", len(buf.tail(100)))
	}
}
