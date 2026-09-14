package railway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func inventoryFixtureNode(id string) inventoryInstance {
	node := inventoryInstance{ServiceID: id, Name: "vmbox-" + id, EnvironmentID: "environment", Active: []inventoryDeployment{}}
	node.Latest = &inventoryDeployment{ID: "deployment-" + id, Status: "SUCCESS"}
	node.Latest.Instances = append(node.Latest.Instances, struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}{"replica-" + id, "RUNNING"})
	return node
}
func writeInventoryPage(w http.ResponseWriter, nodes []inventoryInstance, more bool, cursor any) {
	edges := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		edges = append(edges, map[string]any{"node": node})
	}
	json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"environment": map[string]any{"id": "environment", "projectId": "project", "serviceInstances": map[string]any{"edges": edges, "pageInfo": map[string]any{"hasNextPage": more, "endCursor": cursor}}}}})
}

func TestHTTPServiceInventoryPaginationAndFailurePreservesCache(t *testing.T) {
	requests := 0
	api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Query != serviceInventoryQuery || body.Variables["environmentId"] != "environment" || body.Variables["projectId"] != "project" {
			t.Error("inventory scope mismatch")
		}
		switch requests {
		case 1:
			if body.Variables["after"] != nil {
				t.Error("unexpected first cursor")
			}
			writeInventoryPage(w, []inventoryInstance{inventoryFixtureNode("one")}, true, "next")
		case 2:
			if body.Variables["after"] != "next" {
				t.Error("pagination cursor lost")
			}
			writeInventoryPage(w, []inventoryInstance{inventoryFixtureNode("two")}, false, nil)
		case 3:
			io.WriteString(w, `{"data":{"environment":null}}`)
		default:
			writeInventoryPage(w, []inventoryInstance{}, false, nil)
		}
	})
	runner := &procexec.FakeRunner{}
	p := New(Config{API: api, ProjectID: "project", EnvironmentID: "environment"}, runner)
	items, err := p.services(context.Background())
	if err != nil || len(items) != 2 || serviceState(items[0]) != provider.StateRunning {
		t.Fatal("paginated inventory failed", err)
	}
	if _, err := p.services(context.Background()); err == nil {
		t.Fatal("null inventory accepted")
	}
	if _, ok := p.servicesByKey["one"]; !ok {
		t.Fatal("failed refresh cleared last valid inventory")
	}
	if _, err := p.services(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(p.servicesByKey) != 0 {
		t.Fatal("complete empty inventory retained removed service aliases")
	}
	if len(runner.Calls) != 0 {
		t.Fatal("HTTP inventory used CLI")
	}
}

func TestHTTPServiceInventoryRejectsWrongScopeAndMissingFields(t *testing.T) {
	for _, body := range []string{
		`{"data":{"environment":{"id":"other","projectId":"project","serviceInstances":{"edges":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}`,
		`{"data":{"environment":{"id":"environment","projectId":"project","serviceInstances":{"edges":[]}}}}`,
		`{"data":{"environment":{"id":"environment","projectId":"project","serviceInstances":{"edges":[{"node":{"serviceId":"one","serviceName":"one","environmentId":"environment","activeDeployments":[]}}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}`,
	} {
		api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
		p := New(Config{API: api, ProjectID: "project", EnvironmentID: "environment"}, &procexec.FakeRunner{})
		if _, err := p.services(context.Background()); err == nil {
			t.Fatal("partial or wrong-scope inventory accepted")
		}
	}
}

func TestInventoryServingReplicaState(t *testing.T) {
	node := inventoryFixtureNode("one")
	serving := *node.Latest
	node.Active = []inventoryDeployment{serving}
	node.Latest = &inventoryDeployment{ID: "new-building", Status: "BUILDING"}
	item, err := inventoryService(node)
	if err != nil || serviceState(item) != provider.StateRunning {
		t.Fatal("serving deployment hidden by building deployment", err)
	}
	node.Active[0].Stopped = true
	item, err = inventoryService(node)
	if err != nil || serviceState(item) != provider.StateStopped {
		t.Fatal("stopped deployment appeared running", err)
	}
	node.Active[0].Stopped = false
	node.Active[0].Instances[0].Status = "CRASHED"
	item, err = inventoryService(node)
	if err != nil || serviceState(item) != provider.StateFailed {
		t.Fatal("crashed replica appeared running", err)
	}
	node.Active[0].Instances[0].Status = "REMOVED"
	item, err = inventoryService(node)
	if err != nil || serviceState(item) != provider.StateStopped {
		t.Fatal("removed replica appeared live", err)
	}
}

func TestHTTPBoxListDoesNotHideRateLimitedMetadata(t *testing.T) {
	api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid request")
			return
		}
		if request.Query == serviceInventoryQuery {
			writeInventoryPage(w, []inventoryInstance{inventoryFixtureNode("one")}, false, nil)
			return
		}
		if request.Query != variablesQuery {
			t.Error("unexpected inventory request")
		}
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	})
	p := New(Config{API: api, ProjectID: "project", EnvironmentID: "environment"}, &procexec.FakeRunner{})
	if boxes, err := p.List(context.Background()); err == nil || boxes != nil {
		t.Fatal("rate-limited metadata returned an incomplete fleet as success")
	}
}
