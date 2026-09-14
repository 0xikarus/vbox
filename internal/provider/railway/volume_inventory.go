package railway

import (
	"context"
	"encoding/json"
	"errors"
)

const volumeInventoryQuery = `query($projectId: String!, $after: String) {
 project(id: $projectId) { id volumes(first: 100, after: $after) {
  pageInfo { hasNextPage endCursor }
  edges { node { id name projectId volumeInstances(first: 100) {
   pageInfo { hasNextPage endCursor }
   edges { node { id environmentId serviceId mountPath state isPendingDeletion } }
  } } }
 } }
}`

type volumePageInfo struct {
	More   *bool   `json:"hasNextPage"`
	Cursor *string `json:"endCursor"`
}
type volumeConnection[T any] struct {
	Info  *volumePageInfo `json:"pageInfo"`
	Edges []struct {
		Node *T `json:"node"`
	} `json:"edges"`
}
type volumeInstanceObservation struct {
	ID          string          `json:"id"`
	Environment string          `json:"environmentId"`
	Service     json.RawMessage `json:"serviceId"`
	Mount       *string         `json:"mountPath"`
	State       json.RawMessage `json:"state"`
	Pending     *bool           `json:"isPendingDeletion"`
}
type volumeObservation struct {
	ID        string                                       `json:"id"`
	Name      string                                       `json:"name"`
	Project   string                                       `json:"projectId"`
	Instances *volumeConnection[volumeInstanceObservation] `json:"volumeInstances"`
}

func completeVolumePage[T any](page *volumeConnection[T]) bool {
	return page != nil && page.Info != nil && page.Info.More != nil && page.Edges != nil
}

func (p *Provider) fetchVolumes(ctx context.Context) ([]railwayVolume, error) {
	items := []railwayVolume{}
	seen, cursors := map[string]bool{}, map[string]bool{}
	var after *string
	for page := 0; page < 100; page++ {
		result, err := p.api(ctx, volumeInventoryQuery, map[string]any{"projectId": p.cfg.ProjectID, "after": after})
		if err != nil {
			return nil, err
		}
		var response struct {
			Data struct {
				Project *struct {
					ID      string                               `json:"id"`
					Volumes *volumeConnection[volumeObservation] `json:"volumes"`
				} `json:"project"`
			} `json:"data"`
		}
		if json.Unmarshal(result.Stdout, &response) != nil || response.Data.Project == nil || response.Data.Project.ID != p.cfg.ProjectID || !completeVolumePage(response.Data.Project.Volumes) {
			return nil, errors.New("incomplete or mismatched Railway volume inventory")
		}
		volumes := response.Data.Project.Volumes
		for _, edge := range volumes.Edges {
			node := edge.Node
			if node == nil || node.ID == "" || node.Name == "" || node.Project != p.cfg.ProjectID || seen[node.ID] || !completeVolumePage(node.Instances) || *node.Instances.Info.More {
				return nil, errors.New("incomplete Railway volume instance inventory")
			}
			seen[node.ID] = true
			item := railwayVolume{ID: node.ID, Name: node.Name, Status: "UNKNOWN"}
			environments := map[string]bool{}
			for _, instanceEdge := range node.Instances.Edges {
				instance := instanceEdge.Node
				if instance == nil || instance.ID == "" || instance.Mount == nil || instance.Pending == nil || len(instance.Service) == 0 || len(instance.State) == 0 || environments[instance.Environment] {
					return nil, errors.New("invalid Railway volume instance")
				}
				environments[instance.Environment] = true
				var serviceID *string
				var state *string
				if json.Unmarshal(instance.Service, &serviceID) != nil || json.Unmarshal(instance.State, &state) != nil {
					return nil, errors.New("invalid Railway volume attachment")
				}
				if instance.Environment != p.cfg.EnvironmentID {
					item.OtherEnvironments = true
					continue
				}
				item.InstanceID = instance.ID
				item.MountPath = *instance.Mount
				item.IsPendingDeletion = *instance.Pending
				if state != nil {
					item.Status = *state
				}
				if serviceID != nil {
					if *serviceID == "" {
						return nil, errors.New("invalid attached Railway service ID")
					}
					item.ServiceID = *serviceID
					item.ServiceName = *serviceID
					p.cacheMu.RLock()
					service, ok := p.servicesByKey[*serviceID]
					p.cacheMu.RUnlock()
					if ok {
						item.ServiceName = service.Name
					}
				}
			}
			items = append(items, item)
		}
		if !*volumes.Info.More {
			return items, nil
		}
		cursor := volumes.Info.Cursor
		if cursor == nil || *cursor == "" || cursors[*cursor] || len(volumes.Edges) == 0 {
			return nil, errors.New("invalid Railway volume pagination")
		}
		cursors[*cursor] = true
		after = cursor
	}
	return nil, errors.New("Railway volume inventory page limit exceeded")
}
