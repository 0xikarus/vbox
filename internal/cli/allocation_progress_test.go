package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestResumeWaitShowsActualHibernatePhase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/logical-boxes/box":
			json.NewEncoder(w).Encode(v1.LogicalBox{ID: "box", State: v1.LogicalBoxHibernating, RestorationState: "detaching-volume"})
		case "/v1/allocations/request":
			json.NewEncoder(w).Encode(v1.Allocation{RequestID: "request", State: "ready", Phase: "ready"})
		default:
			t.Error("unexpected request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := New()
	var output bytes.Buffer
	a.Err = &output
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.waitAllocation(ctx, config.Context{Controller: srv.URL}, "test", v1.Allocation{RequestID: "request", LogicalBoxID: "box", State: "queued", Phase: "waiting-for-hibernate"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "hibernating · detaching-volume (resume queued)") || strings.Contains(output.String(), "Starting box") {
		t.Fatalf("progress=%q", output.String())
	}
}
