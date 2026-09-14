package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestWorkerRejectsCredentialRedirectsAndUnsafeURLs(t *testing.T) {
	var redirected atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(500) }))
	defer destination.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	conn := provider.Connection{Transport: "controller-worker", Endpoint: destination.URL, Metadata: map[string]string{"accountId": "account", "boxId": "box", "slotId": "slot", "assignment": "fence", "connectionRevision": "agent"}}
	worker := Worker{ControllerURL: source.URL, Token: "test-token", HTTP: source.Client()}
	if _, err := worker.ExecConnection(context.Background(), conn, []string{"true"}, provider.ExecOptions{}); err == nil {
		t.Fatal("redirect accepted")
	}
	if redirected.Load() != 0 {
		t.Fatal("controller credential sent to redirected host")
	}
	for _, address := range []string{"http://localhost", "https://user:password@localhost", "https://localhost?token=x", "https://localhost#fragment"} {
		worker.ControllerURL = address
		if _, err := worker.ExecConnection(context.Background(), conn, []string{"true"}, provider.ExecOptions{}); err == nil {
			t.Fatalf("unsafe URL accepted: %s", address)
		}
	}
}
