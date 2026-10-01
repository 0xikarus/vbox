package sharedworker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestResourceUsageDoesNotHoldStoreLockDuringInspect(t *testing.T) {
	workspace := Workspace{ID: NewID()}
	store := &Store{
		Runtime: &LinuxRuntime{Root: t.TempDir(), Container: &ContainerRuntime{}},
		state: State{
			Slots:      map[string]Slot{"box": {WorkspaceID: workspace.ID, State: provider.StateRunning}},
			Workspaces: map[string]Workspace{workspace.ID: workspace},
		},
	}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := store.resourceUsageWithInspect(context.Background(), "box", func(context.Context, Workspace) (*containerInspect, error) {
			close(started)
			<-release
			return nil, errors.New("inspect stopped")
		})
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("inspect did not start")
	}
	healthDone := make(chan error, 1)
	go func() { healthDone <- store.Health() }()
	select {
	case err := <-healthDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("parallel store operation blocked on resource inspection")
	}
	close(release)
	if err := <-finished; err == nil || err.Error() != "inspect stopped" {
		t.Fatalf("inspect error = %v", err)
	}
}

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
	if used, observed, _ := store.cachedDiskUsage(workspace, root); used != nil || observed != nil {
		t.Fatal("initial scan should run in the background")
	}
	deadline := time.Now().Add(3 * time.Second)
	var first int64
	for time.Now().Before(deadline) {
		used, observed, _ := store.cachedDiskUsage(workspace, root)
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
	used, _, _ := store.cachedDiskUsage(workspace, root)
	if used == nil || *used != first {
		t.Fatalf("five-minute cache changed during a poll: %v", used)
	}
	if _, err := scanDiskBytes(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := diskFileCount(context.Background(), root, 1); !errors.Is(err, errDiskTooManyFiles) {
		t.Fatalf("file count limit error = %v", err)
	}
}
