package sevalla

import (
	"context"
	"encoding/json"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestValidateAndExactArgvExercise(t *testing.T) {
	var got []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("missing bearer auth")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/resources/clusters":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "cluster"}}, "total": 1, "offset": 0, "limit": 1})
		case "/applications":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "11111111-1111-4111-8111-111111111111", "display_name": "vmbox-box", "name": "vmbox-box", "status": "deploymentSuccess"}}, "total": 1, "offset": 0, "limit": 100})
		case "/applications/11111111-1111-4111-8111-111111111111/processes":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "22222222-2222-4222-8222-222222222222"}}, "total": 1, "offset": 0, "limit": 100})
		case "/applications/11111111-1111-4111-8111-111111111111/processes/22222222-2222-4222-8222-222222222222/exec":
			var body struct {
				Command []string `json:"command"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			got = body.Command
			json.NewEncoder(w).Encode(map[string]any{"stdout": "ok\n", "stderr": "", "exit_code": 0})
		default:
			http.Error(w, r.URL.Path, 404)
		}
	}))
	defer server.Close()
	p := New(Config{Token: "token", APIURL: server.URL, ProjectID: "project", HTTPClient: server.Client()})
	cap, err := p.Validate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cap.AutomatedStorage || !cap.ExactArgv || !reflect.DeepEqual(cap.Architectures, []string{"linux/amd64"}) {
		t.Fatalf("unexpected capabilities: %+v", cap)
	}
	argv := []string{"printf", "%s", `$HOME; rm -rf /`, "two words"}
	var out strings.Builder
	result, err := p.Exec(context.Background(), "box", argv, provider.ExecOptions{Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || out.String() != "ok\n" {
		t.Fatalf("unexpected result %+v %q", result, out.String())
	}
	if !reflect.DeepEqual(got, argv) {
		t.Fatalf("Sevalla argv changed: got %#v want %#v", got, argv)
	}
}

func TestCreateStorageReportsPublicAPIGap(t *testing.T) {
	p := New(Config{})
	_, err := p.CreateStorage(context.Background(), "box", provider.Resources{DiskGiB: 10})
	if err == nil || !strings.Contains(err.Error(), "no application-disk create endpoint") {
		t.Fatalf("unexpected error: %v", err)
	}
}
