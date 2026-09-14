package railway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func volumeInventoryFixture(id string, environments ...string) map[string]any {
	edges := []map[string]any{}
	for _, environment := range environments {
		edges = append(edges, map[string]any{"node": map[string]any{"id": "instance-" + id + "-" + environment, "environmentId": environment, "serviceId": nil, "mountPath": "/data", "state": "READY", "isPendingDeletion": false}})
	}
	return map[string]any{"id": id, "name": id + "-data", "projectId": "project", "volumeInstances": map[string]any{"edges": edges, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}}}
}
func writeVolumeInventory(w http.ResponseWriter, nodes []map[string]any, more bool, cursor any) {
	edges := []map[string]any{}
	for _, node := range nodes {
		edges = append(edges, map[string]any{"node": node})
	}
	json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"project": map[string]any{"id": "project", "volumes": map[string]any{"edges": edges, "pageInfo": map[string]any{"hasNextPage": more, "endCursor": cursor}}}}})
}

func TestHTTPVolumeInventoryPaginationAndCrossEnvironmentDeletionGuard(t *testing.T) {
	for _, crossEnvironment := range []bool{false, true} {
		requests := 0
		api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) {
			requests++
			var request struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.Query != volumeInventoryQuery || request.Variables["projectId"] != "project" {
				t.Error("unexpected volume inventory request")
				return
			}
			if requests == 1 {
				writeVolumeInventory(w, []map[string]any{volumeInventoryFixture("first", "environment")}, true, "second-page")
				return
			}
			if requests == 2 {
				if request.Variables["after"] != "second-page" {
					t.Error("volume pagination cursor lost")
				}
				writeVolumeInventory(w, []map[string]any{volumeInventoryFixture("second", "environment")}, false, nil)
				return
			}
			if crossEnvironment {
				writeVolumeInventory(w, []map[string]any{volumeInventoryFixture("first", "environment", "other-environment")}, false, nil)
			} else {
				writeVolumeInventory(w, []map[string]any{volumeInventoryFixture("first", "other-environment")}, false, nil)
			}
		})
		runner := &procexec.FakeRunner{}
		p := New(Config{API: api, ProjectID: "project", EnvironmentID: "environment"}, runner)
		volumes, err := p.volumes(context.Background())
		if err != nil || len(volumes) != 2 || volumes[0].InstanceID == "" || volumes[0].OtherEnvironments {
			t.Fatal("complete scoped volume inventory failed", err)
		}
		if err := p.DeleteStorage(context.Background(), provider.Storage{ID: "first", Name: "first-data"}, provider.Owner{AccountID: "account", BoxID: "box"}); err == nil {
			t.Fatal("cross-environment volume deletion accepted")
		}
		if requests != 3 || len(runner.Calls) != 0 {
			t.Fatal("guard invoked mutation or CLI")
		}
	}
}

func TestHTTPVolumeInventoryRejectsPartialAttachmentEvidence(t *testing.T) {
	for _, scenario := range []string{"truncated-instances", "missing-service-id", "missing-pages", "unknown-project"} {
		t.Run(scenario, func(t *testing.T) {
			api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) {
				node := volumeInventoryFixture("volume", "environment")
				instances := node["volumeInstances"].(map[string]any)
				switch scenario {
				case "truncated-instances":
					instances["pageInfo"] = map[string]any{"hasNextPage": true, "endCursor": "more"}
				case "missing-service-id":
					delete(instances["edges"].([]map[string]any)[0]["node"].(map[string]any), "serviceId")
				case "missing-pages":
					delete(instances, "pageInfo")
				case "unknown-project":
					node["projectId"] = "other-project"
				}
				writeVolumeInventory(w, []map[string]any{node}, false, nil)
			})
			p := New(Config{API: api, ProjectID: "project", EnvironmentID: "environment"}, &procexec.FakeRunner{})
			if _, err := p.volumes(context.Background()); err == nil {
				t.Fatal("incomplete attachment evidence accepted")
			}
		})
	}
}

func TestVolumeHTTPMutationsScopeAndExplicitDetach(t *testing.T) {
	for _, operation := range []string{"add", "attach", "detach", "delete"} {
		t.Run(operation, func(t *testing.T) {
			calls := 0
			api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body struct {
					Query     string                     `json:"query"`
					Variables map[string]json.RawMessage `json:"variables"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid volume API body")
					return
				}
				var input map[string]any
				_ = json.Unmarshal(body.Variables["input"], &input)
				switch operation {
				case "add":
					if body.Query != volumeCreateMutation || input["projectId"] != "project" || input["environmentId"] != "environment" || input["serviceId"] != "service" || input["mountPath"] != "/data" {
						t.Error("volume creation scope mismatch")
					}
					io.WriteString(w, `{"data":{"volumeCreate":{"id":"volume","name":"volume-name"}}}`)
				case "attach", "detach":
					if body.Query != volumeUpdateMutation || string(body.Variables["volumeId"]) != `"volume"` || string(body.Variables["environmentId"]) != `"environment"` {
						t.Error("attachment update lacks exact environment/volume scope")
					}
					if operation == "attach" {
						if input["serviceId"] != "service" || input["mountPath"] != "/data" {
							t.Error("invalid attachment target")
						}
					} else {
						if value, present := input["serviceId"]; !present || value != nil {
							t.Error("detach must explicitly set serviceId null")
						}
					}
					io.WriteString(w, `{"data":{"volumeInstanceUpdate":true}}`)
				case "delete":
					if body.Query != volumeDeleteMutation || string(body.Variables["volumeId"]) != `"volume"` {
						t.Error("delete lacks exact volume ID")
					}
					io.WriteString(w, `{"data":{"volumeDelete":true}}`)
				}
			})
			runner := &procexec.FakeRunner{}
			p := New(Config{API: api, ProjectID: "project", EnvironmentID: "environment"}, runner)
			if _, err := p.runVolume(context.Background(), "service", nil, operation, "--volume", "volume"); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || len(runner.Calls) != 0 {
				t.Fatal("volume mutation replayed or used CLI")
			}
		})
	}
}

func TestVolumeHTTPRejectsUnconfirmedMutationWithoutReplay(t *testing.T) {
	calls := 0
	api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"data":{"volumeInstanceUpdate":false}}`)
	})
	p := New(Config{API: api, EnvironmentID: "environment"}, &procexec.FakeRunner{})
	_, err := p.runVolume(context.Background(), "service", nil, "detach", "--volume", "volume")
	var failed *APIError
	if !errors.As(err, &failed) || !failed.Ambiguous || calls != 1 {
		t.Fatal("unconfirmed volume mutation accepted or replayed", err)
	}
}
