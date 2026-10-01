package sharedworker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSlotMemoryUsageReadsCgroupCounters(t *testing.T) {
	root := t.TempDir()
	writeResourceFixture(t, root, "memory.current", "1288490188\n")
	writeResourceFixture(t, root, "memory.swap.current", "107374182\n")
	memory, swap, err := slotMemoryUsage(root)
	if err != nil || memory != 1288490188 || swap != 107374182 {
		t.Fatalf("memory=%d swap=%d err=%v", memory, swap, err)
	}
}

func TestDiskScanIsCachedAcrossResourcePolls(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "payload"), make([]byte, 8192), 0600); err != nil {
		t.Fatal(err)
	}
	store := &Store{}
	workspace := Workspace{ID: NewID(), SizeGiB: 2}
	if used, observed := store.cachedDiskUsage(workspace, root); used != nil || observed != nil {
		t.Fatal("initial scan should run in the background")
	}
	deadline := time.Now().Add(3 * time.Second)
	var first int64
	for time.Now().Before(deadline) {
		used, observed := store.cachedDiskUsage(workspace, root)
		if used != nil && observed != nil {
			first = *used
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if first < 8192 {
		t.Fatalf("disk scan did not return file usage: %d", first)
	}
	if err := os.WriteFile(filepath.Join(root, "new-payload"), make([]byte, 1<<20), 0600); err != nil {
		t.Fatal(err)
	}
	used, _ := store.cachedDiskUsage(workspace, root)
	if used == nil || *used != first {
		t.Fatalf("five-minute cache changed during a poll: %v", used)
	}
	if _, err := scanDiskBytes(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := diskFileCount(context.Background(), root, 1); err == nil {
		t.Fatal("file count limit was ignored")
	}
}
