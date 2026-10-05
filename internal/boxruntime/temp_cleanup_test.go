package boxruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupStaleTempRespectsAgeOwnershipAndOpenFiles(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "tmp")
	system := filepath.Join(root, "system-tmp")
	workspace := filepath.Join(root, "workspace", "project")
	for _, path := range []string{private, system, workspace, filepath.Join(root, "home")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	old := now.Add(-StaleTempAge - time.Hour)
	oldFile := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("stale"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	stale := filepath.Join(private, "puppeteer-stale")
	chromiumUnpack := filepath.Join(private, "org.chromium.Chromium.chromium_chrome_Unpacker_BeginUnzipping.a1b2")
	chromiumFetcher := filepath.Join(system, "org.chromium.Chromium.chromium_chrome_url_fetcher_c3d4")
	chromiumHidden := filepath.Join(private, ".org.chromium.Chromium.e5f6")
	active := filepath.Join(private, "chromium-open")
	recent := filepath.Join(system, "chrome-recent")
	for _, path := range []string{stale, chromiumUnpack, chromiumFetcher, chromiumHidden, active, recent} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		oldFile(filepath.Join(path, "payload"))
	}
	for _, path := range []string{stale, chromiumUnpack, chromiumFetcher, chromiumHidden, active} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	core := filepath.Join(workspace, "core.123")
	oldFile(core)
	outside := filepath.Join(root, "keep")
	oldFile(outside)
	if err := os.Symlink(outside, filepath.Join(private, "chrome-escape")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(active, "payload"))
	if err != nil {
		t.Fatal(err)
	}
	key, _ := inode(info)
	result, err := cleanupTempAt(context.Background(), root, private, system, os.Geteuid(), now, map[inodeKey]bool{key: true})
	if err != nil || result.Removed < 9 || result.ReclaimedBytes <= 0 || result.Skipped == 0 {
		t.Fatalf("cleanup result=%+v err=%v", result, err)
	}
	for _, path := range []string{stale, chromiumUnpack, chromiumFetcher, chromiumHidden, core} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("stale path remains %s: %v", path, err)
		}
	}
	for _, path := range []string{active, recent, outside, filepath.Join(private, "chrome-escape")} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("protected path removed %s: %v", path, err)
		}
	}
}
