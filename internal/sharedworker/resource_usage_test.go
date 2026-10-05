package sharedworker

import (
	"context"
	"errors"
	"fmt"
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

func TestWritableLayerSizeRequiresVerifiedContainer(t *testing.T) {
	workspace := Workspace{ID: NewID()}
	data := []byte(fmt.Sprintf(`[{"SizeRw":4096,"Config":{"Labels":{"io.vmbox.workspace":%q,"io.vmbox.root":"/data","io.vmbox.policy":"image:v2-sudo"}}}]`, workspace.ID))
	if size, err := parseWritableSize(data, workspace, "/data", "image"); err != nil || size != 4096 {
		t.Fatalf("writable size=%d err=%v", size, err)
	}
	if _, err := parseWritableSize(data, Workspace{ID: NewID()}, "/data", "image"); err == nil {
		t.Fatal("another workspace's writable layer was accepted")
	}
	if _, err := parseWritableSize([]byte(`[{"SizeRw":null}]`), workspace, "/data", "image"); err == nil {
		t.Fatal("unknown writable layer size was accepted")
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
	writableSize := func(context.Context, Workspace) (int64, error) { return 4096, nil }
	if used, layer, observed, _ := store.cachedDiskUsage(workspace, root, writableSize); used != nil || layer != nil || observed != nil {
		t.Fatal("initial scan should run in the background")
	}
	deadline := time.Now().Add(3 * time.Second)
	var first int64
	for time.Now().Before(deadline) {
		used, layer, observed, _ := store.cachedDiskUsage(workspace, root, writableSize)
		if used != nil && layer != nil && observed != nil {
			if *layer != 4096 {
				t.Fatalf("writable layer=%d", *layer)
			}
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
	used, _, _, _ := store.cachedDiskUsage(workspace, root, writableSize)
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

func TestDiskScanMarksMissingWritableLayerPartial(t *testing.T) {
	root := t.TempDir()
	store := &Store{}
	workspace := Workspace{ID: NewID()}
	fail := func(context.Context, Workspace) (int64, error) { return 0, errors.New("Docker size unavailable") }
	store.cachedDiskUsage(workspace, root, fail)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		used, layer, observed, reason := store.cachedDiskUsage(workspace, root, fail)
		if observed != nil {
			if used == nil || layer != nil || reason == "" {
				t.Fatalf("partial observation used=%v layer=%v reason=%q", used, layer, reason)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("workspace scan did not finish")
}
