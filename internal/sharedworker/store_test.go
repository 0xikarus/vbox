package sharedworker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

type fakeRuntime struct {
	stopped      []string
	stopError    error
	prepared     []string
	prepareError error
}

func (r *fakeRuntime) Prepare(_ context.Context, workspace Workspace) error {
	r.prepared = append(r.prepared, workspace.ID)
	return r.prepareError
}

func TestReopenRestoresWorkspacesBeforeReady(t *testing.T) {
	root := t.TempDir()
	runtime := &fakeRuntime{}
	store, err := Open(root, "account", 2, runtime)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(provider.CreateRequest{Name: "slot-a", Owner: provider.Owner{AccountID: "account", BoxID: "slot-a"}})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := store.CreateStorage(context.Background(), "slot-a", provider.Owner{AccountID: "account", BoxID: "box-a"}, provider.Resources{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	runtime.prepared = nil
	runtime.prepareError = errors.New("identity conflict")
	if reopened, err := Open(root, "account", 2, runtime); err == nil {
		reopened.Close()
		t.Fatal("worker became ready despite recovery failure")
	}
	runtime.prepareError = nil
	runtime.prepared = nil
	reopened, err := Open(root, "account", 2, runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(runtime.prepared) != 1 || runtime.prepared[0] != storage.ID {
		t.Fatalf("workspaces not recovered: %v", runtime.prepared)
	}
	if err := reopened.Health(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateStorageRefusesLowDiskButAllowsExistingWorkspace(t *testing.T) {
	store, err := Open(t.TempDir(), "account", 1, &fakeRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Create(provider.CreateRequest{Name: "slot", Owner: provider.Owner{AccountID: "account", BoxID: "slot"}}); err != nil {
		t.Fatal(err)
	}
	free := int64(4)
	store.Specs = func() (provider.WorkerSpecs, error) {
		return provider.WorkerSpecs{DiskTotalBytes: 100, DiskFreeBytes: free}, nil
	}
	owner := provider.Owner{AccountID: "account", BoxID: "box"}
	if _, err := store.CreateStorage(context.Background(), "slot", owner, provider.Resources{}); err == nil || !strings.Contains(err.Error(), "insufficient worker disk space") {
		t.Fatalf("low-disk creation error = %v", err)
	}
	if len(store.state.Workspaces) != 0 {
		t.Fatal("low-disk refusal allocated a workspace")
	}
	free = 5
	created, err := store.CreateStorage(context.Background(), "slot", owner, provider.Resources{})
	if err != nil {
		t.Fatal(err)
	}
	free = 4
	retried, err := store.CreateStorage(context.Background(), "slot", owner, provider.Resources{})
	if err != nil || retried.ID != created.ID {
		t.Fatalf("idempotent existing workspace retry: %+v %v", retried, err)
	}
}
func (r *fakeRuntime) Stop(_ context.Context, workspace Workspace) error {
	r.stopped = append(r.stopped, workspace.ID)
	return r.stopError
}
func (r *fakeRuntime) Delete(context.Context, Workspace) error { return nil }

func TestTwoSlotsRetainSeparateWorkspaces(t *testing.T) {
	ctx := context.Background()
	runtime := &fakeRuntime{}
	store, err := Open(t.TempDir(), "account", 2, runtime)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"slot-a", "slot-b"} {
		_, err := store.Create(provider.CreateRequest{Name: name, Owner: provider.Owner{AccountID: "account", BoxID: name}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Create(provider.CreateRequest{Name: "slot-c", Owner: provider.Owner{AccountID: "account", BoxID: "slot-c"}}); err == nil {
		t.Fatal("capacity was not enforced")
	}
	first, err := store.CreateStorage(ctx, "slot-a", provider.Owner{AccountID: "account", BoxID: "box-a"}, provider.Resources{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateStorage(ctx, "slot-b", provider.Owner{AccountID: "account", BoxID: "box-b"}, provider.Resources{})
	if err != nil {
		t.Fatal(err)
	}
	firstBox, _ := store.Inspect("slot-a")
	secondBox, _ := store.Inspect("slot-b")
	firstWorkspace, err := store.WorkspaceFor(firstBox.Connection)
	if err != nil {
		t.Fatal(err)
	}
	secondWorkspace, err := store.WorkspaceFor(secondBox.Connection)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || firstWorkspace.UID == secondWorkspace.UID || firstWorkspace.Display == secondWorkspace.Display {
		t.Fatal("workspaces share identities")
	}
	if err := store.Attach(ctx, "slot-b", first); err == nil {
		t.Fatal("occupied slot accepted another workspace")
	}
	if err := store.Release(ctx, "slot-a", &first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WorkspaceFor(firstBox.Connection); err == nil {
		t.Fatal("detached connection remains valid")
	}
	if _, err := store.WorkspaceFor(secondBox.Connection); err != nil {
		t.Fatalf("unrelated slot disrupted: %v", err)
	}
	if err := store.Attach(ctx, "slot-a", first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WorkspaceFor(firstBox.Connection); err == nil {
		t.Fatal("old connection revived")
	}
	resumed, _ := store.Inspect("slot-a")
	workspace, err := store.WorkspaceFor(resumed.Connection)
	if err != nil || workspace != firstWorkspace {
		t.Fatalf("workspace identity changed: %v", err)
	}
}

func TestStopFailureRevokesButRetainsAttachment(t *testing.T) {
	ctx := context.Background()
	runtime := &fakeRuntime{}
	store, err := Open(t.TempDir(), "account", 2, runtime)
	if err != nil {
		t.Fatal(err)
	}
	owner := provider.Owner{AccountID: "account", BoxID: "slot-a"}
	if _, err := store.Create(provider.CreateRequest{Name: "slot-a", Owner: owner}); err != nil {
		t.Fatal(err)
	}
	storage, err := store.CreateStorage(ctx, "slot-a", provider.Owner{AccountID: "account", BoxID: "box-a"}, provider.Resources{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := store.Inspect("slot-a")
	runtime.stopError = errors.New("cannot stop workspace")
	if err := store.Release(ctx, "slot-a", &storage); err == nil {
		t.Fatal("stop failure ignored")
	}
	if _, err := store.WorkspaceFor(before.Connection); err == nil {
		t.Fatal("failed stop did not revoke connection")
	}
	after, _ := store.Inspect("slot-a")
	if after.Storage == nil || after.Storage.ID != storage.ID {
		t.Fatal("failed stop released workspace")
	}
	if err := store.DeleteSlot("slot-a", owner); err == nil {
		t.Fatal("deleted attached slot")
	}
	if err := store.DeleteStorage(ctx, storage, provider.Owner{AccountID: "other", BoxID: "box-a"}); err == nil {
		t.Fatal("wrong owner accepted")
	}
}

func TestRestartInvalidatesConnectionsAndPreservesIdentities(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root, "account", 2, &fakeRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(provider.CreateRequest{Name: "slot-a", Owner: provider.Owner{AccountID: "account", BoxID: "slot-a"}}); err != nil {
		t.Fatal(err)
	}
	storage, err := store.CreateStorage(context.Background(), "slot-a", provider.Owner{AccountID: "account", BoxID: "box-a"}, provider.Resources{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := store.Inspect("slot-a")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(root, "account", 2, &fakeRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.WorkspaceFor(before.Connection); err == nil {
		t.Fatal("previous supervisor connection remains valid")
	}
	after, _ := restarted.Inspect("slot-a")
	if after.Storage == nil || after.Storage.ID != storage.ID {
		t.Fatal("retained workspace lost")
	}
	if _, err := Open(root, "other-account", 2, &fakeRuntime{}); err == nil {
		t.Fatal("retained account identity replaced")
	}
}

func TestPersistenceFailureClosesExecution(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root, "account", 2, &fakeRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := os.Rename(filepath.Join(root, ".shared-worker"), filepath.Join(root, "saved")); err != nil {
		t.Fatal(err)
	}
	request := provider.CreateRequest{Name: "slot-a", Owner: provider.Owner{AccountID: "account", BoxID: "slot-a"}}
	if _, err := store.Create(request); err == nil {
		t.Fatal("persistence failure ignored")
	}
	if err := os.Rename(filepath.Join(root, "saved"), filepath.Join(root, ".shared-worker")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(request); err == nil {
		t.Fatal("failed store resumed without recovery")
	}
}

func TestRecreatedSlotDoesNotReviveConnection(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir(), "account", 2, &fakeRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	request := provider.CreateRequest{Name: "slot-a", Owner: provider.Owner{AccountID: "account", BoxID: "slot-a"}}
	if _, err := store.Create(request); err != nil {
		t.Fatal(err)
	}
	storage, err := store.CreateStorage(ctx, "slot-a", provider.Owner{AccountID: "account", BoxID: "box-a"}, provider.Resources{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := store.Inspect("slot-a")
	if err := store.Release(ctx, "slot-a", &storage); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSlot("slot-a", request.Owner); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(request); err != nil {
		t.Fatal(err)
	}
	if err := store.Attach(ctx, "slot-a", storage); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WorkspaceFor(before.Connection); err == nil {
		t.Fatal("recreated slot revived old connection")
	}
}
