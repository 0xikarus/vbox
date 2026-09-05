package cli

import (
	"bytes"
	"context"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeletionAcceptanceDoesNotClaimCompletion(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Error("unexpected method")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer s.Close()
	var out bytes.Buffer
	a := New()
	a.Err = &out
	if err := a.requestVolumeDeletion(context.Background(), config.Context{Controller: s.URL}, "synthetic", v1.LogicalBox{ID: "box", Name: "test"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "queued") || strings.Contains(out.String(), "completed") {
		t.Fatal(out.String())
	}
}
