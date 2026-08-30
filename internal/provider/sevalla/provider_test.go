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
	"time"
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
			json.NewEncoder(w).Encode([]any{map[string]any{"id": "cluster"}})
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
	p := New(Config{Token: "token", APIURL: server.URL, HTTPClient: server.Client()})
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

func TestDeployWaitsForExactDeployment(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const processID = "22222222-2222-4222-8222-222222222222"
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/applications":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": appID, "display_name": "vmbox-box", "status": "deploymentSuccess", "docker_image": "image"}}})
		case "/applications/" + appID + "/deployments":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["docker_image"] != "image" {
				t.Errorf("deployment image = %#v", body["docker_image"])
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "33333333-3333-4333-8333-333333333333", "status": "waiting"})
		case "/applications/" + appID + "/deployments/33333333-3333-4333-8333-333333333333":
			polls++
			json.NewEncoder(w).Encode(map[string]any{"id": "33333333-3333-4333-8333-333333333333", "status": "success"})
		case "/applications/" + appID:
			json.NewEncoder(w).Encode(map[string]any{"id": appID, "display_name": "vmbox-box", "status": "deploymentSuccess", "docker_image": "image"})
		case "/applications/" + appID + "/env-vars":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"Key": "VMBOX_BOX_ID", "Value": "box"}, map[string]any{"Key": "VMBOX_ACCOUNT_ID", "Value": "standalone"}}})
		case "/applications/" + appID + "/processes":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": processID}}})
		default:
			http.Error(w, r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()
	p := New(Config{Token: "token", APIURL: server.URL, HTTPClient: server.Client()})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.Deploy(ctx, "box", "image"); err != nil {
		t.Fatal(err)
	}
	if polls != 1 {
		t.Fatalf("deployment polls = %d", polls)
	}
}

func TestCreateStorageReportsPublicAPIGap(t *testing.T) {
	p := New(Config{})
	_, err := p.CreateStorage(context.Background(), "box", provider.Resources{DiskGiB: 10})
	if err == nil || !strings.Contains(err.Error(), "no application-disk create endpoint") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateRefusesSameNameOwnedByAnotherAccount(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/applications":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": appID, "display_name": "vmbox-box"}}})
		case "/applications/" + appID + "/env-vars":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{
				map[string]any{"Key": "VMBOX_ACCOUNT_ID", "Value": "other-account"},
				map[string]any{"Key": "VMBOX_BOX_ID", "Value": "box"},
			}})
		default:
			http.Error(w, r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()
	p := New(Config{Token: "token", APIURL: server.URL, HTTPClient: server.Client()})
	_, err := p.Create(context.Background(), provider.CreateRequest{
		Name: "box", Region: "cluster", Owner: provider.Owner{AccountID: "account", BoxID: "box"},
	})
	if err == nil || !strings.Contains(err.Error(), "ownership mismatch") {
		t.Fatalf("Create error = %v, want ownership mismatch", err)
	}
}

func TestCreateSubmitsDockerRegistryCredentialID(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	created := false
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/applications" && r.Method == http.MethodGet:
			if created {
				json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": appID, "display_name": "vmbox-box", "status": "deploymentSuccess"}}})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
			}
		case r.URL.Path == "/applications" && r.Method == http.MethodPost:
			json.NewDecoder(r.Body).Decode(&payload)
			created = true
			json.NewEncoder(w).Encode(map[string]any{"id": appID, "display_name": "vmbox-box"})
		case strings.HasSuffix(r.URL.Path, "/env-vars") && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(map[string]any{})
		case strings.HasSuffix(r.URL.Path, "/env-vars") && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"data": []any{
				map[string]any{"Key": "VMBOX_ACCOUNT_ID", "Value": "standalone"},
				map[string]any{"Key": "VMBOX_BOX_ID", "Value": "box"},
			}})
		case strings.HasSuffix(r.URL.Path, "/processes"):
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		case strings.HasSuffix(r.URL.Path, "/deployments") && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(map[string]any{"id": "deployment", "status": "success"})
		case r.URL.Path == "/applications/"+appID:
			json.NewEncoder(w).Encode(map[string]any{"id": appID, "display_name": "vmbox-box", "status": "deploymentSuccess"})
		default:
			http.Error(w, r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()
	p := New(Config{Token: "token", APIURL: server.URL, HTTPClient: server.Client(), ProjectID: "project", ClusterID: "cluster", DockerRegistryCredentialID: "registry-credential"})
	if _, err := p.Create(context.Background(), provider.CreateRequest{Name: "box", Owner: provider.Owner{AccountID: "standalone", BoxID: "box"}}); err != nil {
		t.Fatal(err)
	}
	if payload["docker_registry_credential_id"] != "registry-credential" {
		t.Fatalf("payload=%#v", payload)
	}
}
