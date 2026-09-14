package railway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// One environment-scoped, paginated inventory includes serving replica health.
// Fields are verified against the public Railway CLI GraphQL schema.
const serviceInventoryQuery = `query($environmentId: String!, $projectId: String!, $after: String) {
 environment(id: $environmentId, projectId: $projectId) {
  id projectId
  serviceInstances(first: 100, after: $after) {
   pageInfo { hasNextPage endCursor }
   edges { node {
    serviceId serviceName environmentId createdAt updatedAt region numReplicas source { image }
    latestDeployment { id status deploymentStopped instances { id status } }
    activeDeployments { id status deploymentStopped instances { id status } }
   } }
  }
 }
}`

type inventoryDeployment struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Stopped   bool   `json:"deploymentStopped"`
	Instances []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"instances"`
}
type inventoryInstance struct {
	ServiceID     string    `json:"serviceId"`
	Name          string    `json:"serviceName"`
	EnvironmentID string    `json:"environmentId"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Region        string    `json:"region"`
	NumReplicas   *int      `json:"numReplicas"`
	Source        struct {
		Image string `json:"image"`
	} `json:"source"`
	Latest *inventoryDeployment  `json:"latestDeployment"`
	Active []inventoryDeployment `json:"activeDeployments"`
}

func (n *inventoryInstance) UnmarshalJSON(data []byte) error {
	type plain inventoryInstance
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"serviceId", "serviceName", "environmentId", "createdAt", "updatedAt", "region", "numReplicas", "source", "latestDeployment", "activeDeployments"} {
		if _, ok := fields[key]; !ok {
			return errors.New("incomplete Railway service instance")
		}
	}
	*n = inventoryInstance(value)
	return nil
}

func (d *inventoryDeployment) UnmarshalJSON(data []byte) error {
	type plain inventoryDeployment
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"id", "status", "deploymentStopped", "instances"} {
		if raw, ok := fields[key]; !ok || string(raw) == "null" {
			return errors.New("incomplete Railway deployment observation")
		}
	}
	*d = inventoryDeployment(value)
	return nil
}

func (p *Provider) fetchServices(ctx context.Context) ([]service, error) {
	items := []service{}
	seen, cursors := map[string]bool{}, map[string]bool{}
	var after *string
	for page := 0; page < 100; page++ {
		result, err := p.api(ctx, serviceInventoryQuery, map[string]any{"environmentId": p.cfg.EnvironmentID, "projectId": p.cfg.ProjectID, "after": after})
		if err != nil {
			return nil, err
		}
		var response struct {
			Data struct {
				Environment *struct {
					ID        string `json:"id"`
					ProjectID string `json:"projectId"`
					Instances *struct {
						PageInfo *struct {
							More   *bool   `json:"hasNextPage"`
							Cursor *string `json:"endCursor"`
						} `json:"pageInfo"`
						Edges []struct {
							Node *inventoryInstance `json:"node"`
						} `json:"edges"`
					} `json:"serviceInstances"`
				} `json:"environment"`
			} `json:"data"`
		}
		if json.Unmarshal(result.Stdout, &response) != nil {
			return nil, errors.New("invalid Railway service inventory")
		}
		env := response.Data.Environment
		if env == nil || env.ID != p.cfg.EnvironmentID || env.ProjectID != p.cfg.ProjectID || env.Instances == nil || env.Instances.Edges == nil || env.Instances.PageInfo == nil || env.Instances.PageInfo.More == nil {
			return nil, errors.New("incomplete or mismatched Railway service inventory")
		}
		for _, edge := range env.Instances.Edges {
			node := edge.Node
			if node == nil || node.ServiceID == "" || node.Name == "" || node.EnvironmentID != p.cfg.EnvironmentID || seen[node.ServiceID] || node.Active == nil {
				return nil, errors.New("invalid Railway service identity")
			}
			seen[node.ServiceID] = true
			item, err := inventoryService(*node)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		if !*env.Instances.PageInfo.More {
			return items, nil
		}
		cursor := env.Instances.PageInfo.Cursor
		if cursor == nil || *cursor == "" || cursors[*cursor] || len(env.Instances.Edges) == 0 {
			return nil, errors.New("invalid Railway inventory pagination")
		}
		cursors[*cursor] = true
		after = cursor
	}
	return nil, errors.New("Railway service inventory page limit exceeded")
}

func inventoryService(node inventoryInstance) (service, error) {
	item := service{ID: node.ServiceID, Name: node.Name, CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt, Status: "NO_DEPLOYMENT"}
	item.Source.Image = node.Source.Image
	if node.Region != "" {
		item.Regions = append(item.Regions, struct {
			Name string `json:"name"`
		}{node.Region})
	}
	selected := node.Latest
	// Match Railway CLI's preference for a stable serving deployment over a new
	// deployment that is still building. Never hide a stopped replica behind a
	// historical SUCCESS deployment status.
	for i := range node.Active {
		if node.Active[i].Status == "SUCCESS" || node.Active[i].Status == "SLEEPING" || node.Active[i].Status == "CRASHED" {
			selected = &node.Active[i]
			break
		}
	}
	if selected == nil {
		return item, nil
	}
	if selected.ID == "" || selected.Status == "" || selected.Instances == nil {
		return service{}, errors.New("incomplete Railway replica inventory")
	}
	item.Status = selected.Status
	if selected.Stopped {
		item.Status = "REMOVED"
	}
	item.Replicas = &serviceReplicas{Configured: 1}
	if node.NumReplicas != nil {
		item.Replicas.Configured = *node.NumReplicas
	}
	for _, instance := range selected.Instances {
		if instance.ID == "" || instance.Status == "" {
			return service{}, errors.New("incomplete Railway replica state")
		}
		if instance.Status == "REMOVED" || instance.Status == "REMOVING" {
			continue
		}
		item.Replicas.Total++
		switch strings.ToUpper(instance.Status) {
		case "RUNNING":
			item.Replicas.Running++
		case "CRASHED":
			item.Replicas.Crashed++
		case "EXITED":
			item.Replicas.Exited++
		}
	}
	return item, nil
}
