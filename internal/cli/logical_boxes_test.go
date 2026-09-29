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

func TestDeleteWithoutConnecting(t *testing.T) {
	for _, tc := range []struct{ name, keys, method, path string }{
		{"delete", "research\n", "DELETE", "/v1/logical-boxes/box-id/volume"},
		{"cancel-delete", "wrong\n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutations := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && (r.URL.Path == "/v1/logical-boxes/research" || r.URL.Path == "/v1/logical-boxes/box-id") {
					json.NewEncoder(w).Encode(v1.LogicalBox{ID: "box-id", Name: "research", State: v1.LogicalBoxHibernated})
					return
				}
				if tc.method != "" && r.Method == tc.method && r.URL.Path == tc.path {
					mutations++
					if r.Method == "DELETE" {
						var body map[string]string
						json.NewDecoder(r.Body).Decode(&body)
						if body["confirmation"] != "research" {
							t.Error("missing confirmation")
						}
					}
					json.NewEncoder(w).Encode(v1.LogicalBox{Name: "research", State: v1.LogicalBoxHibernated})
					return
				}
				t.Errorf("unexpected request (must not allocate/connect): %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}))
			defer s.Close()
			a := New()
			a.In, a.Out, a.Err = strings.NewReader(tc.keys), &bytes.Buffer{}, &bytes.Buffer{}
			a.IsTerminal = func() bool { return true }
			if err := a.controllerBoxes(context.Background(), config.Context{Controller: s.URL}, "synthetic", []string{"delete", "research"}); err != nil {
				t.Fatal(err)
			}
			want := 0
			if tc.method != "" {
				want = 1
			}
			if mutations != want {
				t.Fatalf("mutations=%d want %d", mutations, want)
			}
		})
	}
}

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
	for _, expected := range []string{"hibernate accepted", "research-data (volume-1) is retained", "continues after this CLI exits", "vbox status research"} {
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
