package standalone

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChromiumStaleLockCleanupPreservesLiveProfile(t *testing.T) {
	home := t.TempDir()
	proc := t.TempDir()
	profile := filepath.Join(home, ".config/vmbox/chromium")
	os.MkdirAll(profile, 0700)
	for _, n := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		if err := os.Symlink("old-container-123", filepath.Join(profile, n)); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(profile, "Preferences"), []byte("profile data"), 0600)
	os.MkdirAll(filepath.Join(proc, "123"), 0700)
	cmd := filepath.Join(proc, "123/cmdline")
	os.WriteFile(cmd, []byte("chromium\x00--user-data-dir="+profile+"\x00"), 0600)
	if err := cleanupChromiumLocks(home, proc); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(profile, "SingletonLock")); err != nil {
		t.Fatal("removed live profile lock")
	}
	os.Remove(cmd)
	if err := cleanupChromiumLocks(home, proc); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(profile, "SingletonLock")); !os.IsNotExist(err) {
		t.Fatal("stale lock remains")
	}
	if b, err := os.ReadFile(filepath.Join(profile, "Preferences")); err != nil || string(b) != "profile data" {
		t.Fatal("profile data changed")
	}
}
