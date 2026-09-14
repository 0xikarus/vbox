package workeragent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

func assignmentFixture(t *testing.T) (*Agent, workerprotocol.Binding) {
	t.Helper()
	binding := workerprotocol.Binding{AccountID: "account", SlotID: "slot", BoxID: "old", Assignment: "fence-old", Incarnation: "process"}
	path := filepath.Join(t.TempDir(), "binding")
	data, _ := json.Marshal(binding)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return &Agent{Config: Config{AccountID: "account", SlotID: "slot", BindingFile: path}, Incarnation: "process"}, binding
}
func TestAssignmentCompareAndSwapAndLostAcknowledgement(t *testing.T) {
	a, old := assignmentFixture(t)
	next := old
	next.BoxID = "new"
	next.Assignment = "fence-new"
	if err := a.replaceBinding(context.Background(), old, next); err != nil {
		t.Fatal(err)
	}
	if err := a.validateBinding(context.Background(), old); err == nil {
		t.Fatal("old assignment remains authorized")
	}
	if err := a.validateBinding(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if err := a.replaceBinding(context.Background(), old, next); err != nil {
		t.Fatalf("lost acknowledgement recovery: %v", err)
	}
	stale := next
	stale.BoxID = "third"
	stale.Assignment = "fence-third"
	if err := a.replaceBinding(context.Background(), old, stale); err == nil {
		t.Fatal("stale expected binding accepted")
	}
	info, err := os.Stat(a.Config.BindingFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("binding lost private permissions")
	}
}
func TestAssignmentRejectsCrossScopeAndCancelledUpdates(t *testing.T) {
	for _, change := range []func(*workerprotocol.Binding){
		func(b *workerprotocol.Binding) { b.AccountID = "other" },
		func(b *workerprotocol.Binding) { b.SlotID = "other" },
		func(b *workerprotocol.Binding) { b.Incarnation = "old-process" },
		func(b *workerprotocol.Binding) { b.Assignment = "" },
		func(b *workerprotocol.Binding) { b.BoxID = "" },
	} {
		a, old := assignmentFixture(t)
		next := old
		change(&next)
		if err := a.replaceBinding(context.Background(), old, next); err == nil {
			t.Fatal("invalid scope accepted")
		}
		if err := a.validateBinding(context.Background(), old); err != nil {
			t.Fatal("rejected update changed binding")
		}
	}
	a, old := assignmentFixture(t)
	next := old
	next.Assignment = "new"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.replaceBinding(ctx, old, next); err == nil {
		t.Fatal("cancelled update accepted")
	}
	if err := a.validateBinding(context.Background(), old); err != nil {
		t.Fatal("cancelled update changed binding")
	}
}
func TestConcurrentAssignmentUpdatesHaveOneWinner(t *testing.T) {
	a, old := assignmentFixture(t)
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, fence := range []string{"one", "two"} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			next := old
			next.Assignment = fence
			results <- a.replaceBinding(context.Background(), old, next)
		}()
	}
	wait.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent winners=%d", winners)
	}
}
