package shared

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/sharedworker"
)

func TestLifecycleThroughAuthenticatedRPC(t *testing.T) {
	runtime := &sharedworker.LinuxRuntime{Root: t.TempDir()}
	store, err := sharedworker.Open(runtime.Root, "account", 2, runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	token := strings.Repeat("disposable-", 4)
	handler, err := sharedworker.NewServer(store, runtime, token)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := New(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	owner := provider.Owner{AccountID: "account", BoxID: "compute-slot:one"}
	box, err := client.Create(ctx, provider.CreateRequest{Name: "slot-one", Owner: owner})
	if err != nil {
		t.Fatal(err)
	}
	if box.Connection.Transport != "shared-worker" {
		t.Fatal("missing transport")
	}
	if err := client.Delete(ctx, box.ID, provider.Owner{AccountID: "other", BoxID: owner.BoxID}); err == nil {
		t.Fatal("wrong owner accepted")
	}
	if err := client.Delete(ctx, box.ID, owner); err != nil {
		t.Fatal(err)
	}
	badClient, _ := New(server.URL, strings.Repeat("wrong", 8))
	if _, err := badClient.List(ctx); err == nil {
		t.Fatal("wrong token accepted")
	}
}

func TestEndpointValidation(t *testing.T) {
	for _, endpoint := range []string{"http://worker.example", "https://user:secret@worker.example", "https://worker.example/path", "https://worker.example?token=x", "file:///worker"} {
		if _, err := New(endpoint, strings.Repeat("x", 32)); err == nil {
			t.Fatalf("accepted unsafe endpoint %q", endpoint)
		}
	}
}
