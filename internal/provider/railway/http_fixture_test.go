package railway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/procexec"
)

type apiRoundTripper func(*http.Request) (*http.Response, error)

func (f apiRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Existing provider scenario fixtures describe expected GraphQL documents in
// their command sequence. Adapt only the test fixture: production never invokes
// `railway api`, and dedicated HTTP tests assert real request/header semantics.
func newTestProvider(cfg Config, runner procexec.Runner) *Provider {
	serviceIDs := map[string]string{}
	cfg.API = &HTTPAPI{Token: "scenario-fixture", TokenEnvironment: "RAILWAY_API_TOKEN", Budget: NewLocalRequestBudget(time.Nanosecond, 100000), Client: &http.Client{Transport: apiRoundTripper(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Query     string          `json:"query"`
			Variables json.RawMessage `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		result, err := runner.Run(r.Context(), []string{"railway", "api", body.Query, "--variables", string(body.Variables), "--compact"}, nil, nil, nil)
		if err != nil {
			return nil, err
		}
		status := 200
		if result.ExitCode != 0 {
			status = 500
		}
		data := string(result.Stdout)
		if body.Query == volumeInventoryQuery {
			var payload struct {
				Volumes []railwayVolume `json:"volumes"`
			}
			if json.Unmarshal(result.Stdout, &payload) != nil || payload.Volumes == nil {
				_ = json.Unmarshal(result.Stdout, &payload.Volumes)
			}
			edges := make([]map[string]any, 0, len(payload.Volumes))
			for _, volume := range payload.Volumes {
				name := volume.Name
				if name == "" {
					name = volume.ServiceName + "-data"
				}
				status := volume.Status
				if status == "" {
					status = "READY"
				}
				var serviceID any
				if volume.ServiceName != "" {
					id := serviceIDs[volume.ServiceName]
					if id == "" {
						id = volume.ServiceName
					}
					serviceID = id
				}
				instance := map[string]any{"id": "instance-" + volume.ID, "environmentId": cfg.EnvironmentID, "serviceId": serviceID, "mountPath": volume.MountPath, "state": status, "isPendingDeletion": volume.IsPendingDeletion}
				instances := map[string]any{"edges": []map[string]any{{"node": instance}}, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}}
				edges = append(edges, map[string]any{"node": map[string]any{"id": volume.ID, "name": name, "projectId": cfg.ProjectID, "volumeInstances": instances}})
			}
			encoded, _ := json.Marshal(map[string]any{"data": map[string]any{"project": map[string]any{"id": cfg.ProjectID, "volumes": map[string]any{"edges": edges, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}}}}})
			data = string(encoded)
		}
		if strings.TrimSpace(data) == "" {
			if body.Query == deploymentRemoveMutation {
				data = `{"data":{"deploymentRemove":true}}`
			}
			if body.Query == serviceDeleteMutation {
				data = `{"data":{"serviceDelete":true}}`
			}
		}
		if body.Query == volumeCreateMutation {
			data = `{"data":{"volumeCreate":` + data + `}}`
		}
		if body.Query == volumeUpdateMutation && strings.TrimSpace(data) == "" {
			data = `{"data":{"volumeInstanceUpdate":true}}`
		}
		if body.Query == volumeDeleteMutation && strings.TrimSpace(data) == "" {
			data = `{"data":{"volumeDelete":true}}`
		}
		if body.Query == serviceInventoryQuery {
			var services []service
			_ = json.Unmarshal(result.Stdout, &services)
			edges := make([]map[string]any, 0, len(services))
			for _, item := range services {
				serviceIDs[item.Name] = item.ID
				node := inventoryInstance{ServiceID: item.ID, Name: item.Name, EnvironmentID: cfg.EnvironmentID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, Active: []inventoryDeployment{}}
				node.Source.Image = item.Source.Image
				if len(item.Regions) > 0 {
					node.Region = item.Regions[0].Name
				}
				if item.Status != "" && item.Status != "NO_DEPLOYMENT" {
					deployment := &inventoryDeployment{ID: "deployment-" + item.ID, Status: item.Status}
					deployment.Instances = make([]struct {
						ID     string `json:"id"`
						Status string `json:"status"`
					}, 0)
					replicas := item.Replicas
					if replicas == nil {
						replicas = &serviceReplicas{Configured: 1, Running: 1, Total: 1}
					}
					node.NumReplicas = &replicas.Configured
					for state, count := range map[string]int{"RUNNING": replicas.Running, "CRASHED": replicas.Crashed, "EXITED": replicas.Exited} {
						for i := 0; i < count; i++ {
							deployment.Instances = append(deployment.Instances, struct {
								ID     string `json:"id"`
								Status string `json:"status"`
							}{ID: state + "-fixture", Status: state})
						}
					}
					node.Latest = deployment
				}
				edges = append(edges, map[string]any{"node": node})
			}
			encoded, _ := json.Marshal(map[string]any{"data": map[string]any{"environment": map[string]any{"id": cfg.EnvironmentID, "projectId": cfg.ProjectID, "serviceInstances": map[string]any{"edges": edges, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}}}}})
			data = string(encoded)
		}
		if body.Query == serviceDeployMutation {
			encoded, _ := json.Marshal(map[string]any{"data": map[string]any{"serviceInstanceDeployV2": deploymentID(result.Stdout)}})
			data = string(encoded)
		}
		if body.Query == deploymentsQuery || body.Query == deploymentStatusQuery {
			var items []railwayDeployment
			_ = json.Unmarshal(result.Stdout, &items)
			if body.Query == deploymentsQuery {
				edges := make([]map[string]any, 0, len(items))
				for _, item := range items {
					edges = append(edges, map[string]any{"node": item})
				}
				encoded, _ := json.Marshal(map[string]any{"data": map[string]any{"deployments": map[string]any{"edges": edges}}})
				data = string(encoded)
			} else {
				var variables struct {
					ID string `json:"id"`
				}
				_ = json.Unmarshal(body.Variables, &variables)
				var selected *railwayDeployment
				for _, item := range items {
					if item.ID == variables.ID {
						copy := item
						selected = &copy
					}
				}
				encoded, _ := json.Marshal(map[string]any{"data": map[string]any{"deployment": selected}})
				data = string(encoded)
			}
		}
		if body.Query == variablesQuery {
			data = `{"data":{"variables":` + data + `}}`
		}
		if body.Query == variablesUpsertMutation && strings.TrimSpace(data) == "" {
			data = `{"data":{"variableCollectionUpsert":true}}`
		}
		if strings.TrimSpace(data) == "" {
			data = `{"data":{}}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})}}
	return New(cfg, runner)
}
