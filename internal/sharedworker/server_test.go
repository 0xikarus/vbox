package sharedworker

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestRPCCreateReportsIsolationMetadata(t *testing.T) {
	store, err := Open(t.TempDir(), "account", 2, &fakeRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	token := strings.Repeat("t", 32)
	server, err := NewServer(store, &LinuxRuntime{}, token)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	call := func(body Request) Response {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, httpServer.URL+"/v1/rpc", bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("unexpected status %d", response.StatusCode)
		}
		var decoded Response
		if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	created := call(Request{Operation: "create", Create: provider.CreateRequest{Name: "slot-a", Owner: provider.Owner{AccountID: "account", BoxID: "slot-a"}}})
	if created.Error != "" {
		t.Fatal(created.Error)
	}
	inspected := call(Request{Operation: "inspect", ID: "slot-a"})
	if inspected.Error != "" {
		t.Fatal(inspected.Error)
	}
	if got := inspected.Box.Labels["isolation.tier"]; got != string(IsolationTierUID) {
		t.Fatalf("label isolation.tier = %q, labels %v", got, inspected.Box.Labels)
	}
	metadata := inspected.Box.Connection.Metadata
	if got := metadata["isolationTier"]; got != string(IsolationTierUID) {
		t.Fatalf("metadata isolationTier = %q, metadata %v", got, metadata)
	}
	if strings.TrimSpace(metadata["isolationReason"]) == "" {
		t.Fatal("uid tier must report a non-empty isolationReason")
	}
}
