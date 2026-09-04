package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestControllerHibernateReportsAcceptedBackgroundProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/logical-boxes/research/hibernate" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(v1.LogicalBox{
			Name: "research", State: v1.LogicalBoxHibernating,
			VolumeName: "research-data", VolumeID: "volume-1",
		})
	}))
	defer server.Close()

	app := New()
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	if err := app.controllerBoxes(context.Background(), config.Context{Controller: server.URL}, "secret", []string{"hibernate", "research"}); err != nil {
		t.Fatal(err)
	}
	output := app.Err.(*bytes.Buffer).String()
	for _, expected := range []string{"hibernate accepted", "research-data (volume-1) is retained", "continues after this CLI exits", "vmbox status research"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output missing %q: %s", expected, output)
		}
	}
	if strings.Contains(output, "slot was freed") || strings.Contains(output, "hibernated \"") {
		t.Fatalf("accepted request claimed completion: %s", output)
	}
}

func TestControllerHibernateReportsAlreadyCompletedState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v1.LogicalBox{
			Name: "research", State: v1.LogicalBoxHibernated,
			VolumeName: "research-data", VolumeID: "volume-1",
		})
	}))
	defer server.Close()

	app := New()
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	if err := app.controllerBoxes(context.Background(), config.Context{Controller: server.URL}, "secret", []string{"hibernate", "research"}); err != nil {
		t.Fatal(err)
	}
	output := app.Err.(*bytes.Buffer).String()
	if !strings.Contains(output, "hibernated \"research\"") || !strings.Contains(output, "compute slot was freed") {
		t.Fatalf("completed output=%s", output)
	}
}
