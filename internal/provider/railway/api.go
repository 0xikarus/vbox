package railway

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

const (
	deploymentRemoveMutation = `mutation($id: String!) { deploymentRemove(id: $id) }`
	serviceDeleteMutation    = `mutation($id: String!) { serviceDelete(id: $id) }`
	deploymentsQuery         = `query($input: DeploymentListInput!, $first: Int!) { deployments(input: $input, first: $first) { edges { node { id status createdAt } } } }`
	deploymentStatusQuery    = `query($id: String!) { deployment(id: $id) { id status createdAt } }`
	serviceDeployMutation    = `mutation($serviceId: String!, $environmentId: String!) { serviceInstanceDeployV2(serviceId: $serviceId, environmentId: $environmentId) }`
	variablesQuery           = `query($projectId: String!, $environmentId: String!, $serviceId: String!) { variables(projectId: $projectId, environmentId: $environmentId, serviceId: $serviceId) }`
	variablesUpsertMutation  = `mutation($input: VariableCollectionUpsertInput!) { variableCollectionUpsert(input: $input) }`
	serviceCreateMutation    = `mutation($input: ServiceCreateInput!) { serviceCreate(input: $input) { id name } }`
	serviceUpdateMutation    = `mutation($serviceId: String!, $environmentId: String!, $input: ServiceInstanceUpdateInput!) { serviceInstanceUpdate(serviceId: $serviceId, environmentId: $environmentId, input: $input) }`
	limitsUpdateMutation     = `mutation($input: ServiceInstanceLimitsUpdateInput!) { serviceInstanceLimitsUpdate(input: $input) }`
	limitsQuery              = `query($serviceId: String!, $environmentId: String!) { serviceInstanceLimits(serviceId: $serviceId, environmentId: $environmentId) }`
	serviceInstanceQuery     = `query($serviceId: String!, $environmentId: String!) { serviceInstance(serviceId: $serviceId, environmentId: $environmentId) { latestDeployment { deploymentStopped instances { id status } } } }`
)

func (p *Provider) deploymentStatus(ctx context.Context, id string) (string, error) {
	result, err := p.api(ctx, deploymentStatusQuery, map[string]any{"id": id})
	if err != nil {
		return "", err
	}
	var response struct {
		Data struct {
			Deployment *railwayDeployment `json:"deployment"`
		} `json:"data"`
	}
	if json.Unmarshal(result.Stdout, &response) != nil {
		return "", fmt.Errorf("invalid Railway deployment response")
	}
	if response.Data.Deployment == nil {
		return "NOT_VISIBLE", nil
	}
	if response.Data.Deployment.ID != id || response.Data.Deployment.Status == "" {
		return "", fmt.Errorf("Railway deployment identity mismatch")
	}
	return strings.ToUpper(response.Data.Deployment.Status), nil
}

func (p *Provider) upsertVariables(ctx context.Context, serviceID string, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	result, err := p.api(ctx, variablesUpsertMutation, map[string]any{"input": map[string]any{"projectId": p.cfg.ProjectID, "environmentId": p.cfg.EnvironmentID, "serviceId": serviceID, "variables": values, "replace": false, "skipDeploys": true}})
	if err != nil {
		return err
	}
	var response struct {
		Data struct {
			Updated bool `json:"variableCollectionUpsert"`
		} `json:"data"`
	}
	if json.Unmarshal(result.Stdout, &response) != nil || !response.Data.Updated {
		return &APIError{Status: 200, Ambiguous: true}
	}
	return nil
}

func (p *Provider) createService(ctx context.Context, name string) (procexec.Result, error) {
	input := map[string]any{"projectId": p.cfg.ProjectID, "environmentId": p.cfg.EnvironmentID, "name": name}
	return p.api(ctx, serviceCreateMutation, map[string]any{"input": input})
}

func (p *Provider) api(ctx context.Context, document string, variables any) (procexec.Result, error) {
	if p.cfg.Inventory != nil && strings.HasPrefix(strings.TrimSpace(document), "mutation") {
		key := p.inventoryKey()
		p.cfg.Inventory.invalidate(key)
		// Even an ambiguous mutation may have changed infrastructure.
		defer p.cfg.Inventory.invalidate(key)
	}
	data, err := p.cfg.API.Do(ctx, document, variables)
	return procexec.Result{Stdout: data}, err
}

func (p *Provider) serviceInstanceID(ctx context.Context, serviceID string) (string, error) {
	result, err := p.api(ctx, serviceInstanceQuery, map[string]any{
		"serviceId": serviceID, "environmentId": p.cfg.EnvironmentID,
	})
	if err != nil || result.ExitCode != 0 {
		return "", railwayError("resolve deployment instance", result, err)
	}
	var response struct {
		Data struct {
			ServiceInstance struct {
				LatestDeployment struct {
					DeploymentStopped bool `json:"deploymentStopped"`
					Instances         []struct {
						ID     string `json:"id"`
						Status string `json:"status"`
					} `json:"instances"`
				} `json:"latestDeployment"`
			} `json:"serviceInstance"`
		} `json:"data"`
	}
	if err := json.Unmarshal(result.Stdout, &response); err != nil {
		return "", fmt.Errorf("decode Railway deployment instance: %w", err)
	}
	deployment := response.Data.ServiceInstance.LatestDeployment
	if !deployment.DeploymentStopped {
		for _, instance := range deployment.Instances {
			if strings.EqualFold(instance.Status, "RUNNING") && strings.TrimSpace(instance.ID) != "" {
				return instance.ID, nil
			}
		}
	}
	return "", fmt.Errorf("Railway returned no running deployment instance for service %s", serviceID)
}

func (p *Provider) connectImage(ctx context.Context, serviceID, image string) error {
	variables := map[string]any{
		"serviceId":     serviceID,
		"environmentId": p.cfg.EnvironmentID,
		"input": map[string]any{
			"source": map[string]any{"image": image},
		},
	}
	result, err := p.api(ctx, serviceUpdateMutation, variables)
	if err != nil || result.ExitCode != 0 {
		return railwayError("connect image source", result, err)
	}
	return nil
}

func regionConfig(region string) map[string]any {
	regions := map[string]any{"pdx": nil, "ams": nil, "sfo": nil, "iad": nil, "sin": nil}
	for _, id := range []string{"us-west2", "us-east4-eqdc4a", "europe-west4-drams3a", "asia-southeast1-eqsg3a"} {
		regions[id] = nil
	}
	regions[region] = map[string]any{"numReplicas": 1}
	return regions
}

func (p *Provider) setRegion(ctx context.Context, serviceID, region string) error {
	if region == "" {
		return nil
	}
	return p.updateServiceSettings(ctx, serviceID, map[string]any{"multiRegionConfig": regionConfig(region)})
}

// Configure a new service in one mutation before submitting its deployment.
func (p *Provider) configureService(ctx context.Context, serviceID, image, region, command string) error {
	input := map[string]any{"source": map[string]any{"image": image}, "startCommand": command}
	if region != "" {
		input["multiRegionConfig"] = regionConfig(region)
	}
	return p.updateServiceSettings(ctx, serviceID, input)
}

func (p *Provider) updateServiceSettings(ctx context.Context, serviceID string, input map[string]any) error {
	variables := map[string]any{"serviceId": serviceID, "environmentId": p.cfg.EnvironmentID, "input": input}
	result, err := p.api(ctx, serviceUpdateMutation, variables)
	if err != nil || result.ExitCode != 0 {
		return railwayError("configure service", result, err)
	}
	return nil
}

func (p *Provider) deleteVolume(ctx context.Context, volumeID string) error {
	result, err := p.runVolume(ctx, "", nil, "delete", "--volume", volumeID, "--yes", "--json")
	if err != nil || result.ExitCode != 0 {
		return railwayError("delete owned volume", result, err)
	}
	return nil
}

func (p *Provider) setResources(ctx context.Context, serviceID string, resources provider.Resources) error {
	input := map[string]any{"serviceId": serviceID, "environmentId": p.cfg.EnvironmentID}
	if resources.CPU > 0 {
		input["vCPUs"] = resources.CPU
	}
	if resources.MemoryMiB > 0 {
		input["memoryGB"] = float64(resources.MemoryMiB) / 1024
	}
	if len(input) == 2 {
		return nil
	}
	result, err := p.api(ctx, limitsUpdateMutation, map[string]any{"input": input})
	if err != nil || result.ExitCode != 0 {
		return railwayError("set resource limits", result, err)
	}
	return nil
}

func (p *Provider) ResourceLimits(ctx context.Context, serviceID string) (provider.Resources, error) {
	return p.resources(ctx, serviceID)
}

func (p *Provider) SetResourceLimits(ctx context.Context, serviceID string, resources provider.Resources) error {
	return p.setResources(ctx, serviceID, resources)
}

func (p *Provider) resources(ctx context.Context, serviceID string) (provider.Resources, error) {
	variables := map[string]any{"serviceId": serviceID, "environmentId": p.cfg.EnvironmentID}
	result, err := p.api(ctx, limitsQuery, variables)
	if err != nil || result.ExitCode != 0 {
		return provider.Resources{}, railwayError("read resource limits", result, err)
	}
	var response struct {
		Data struct {
			Limits json.RawMessage `json:"serviceInstanceLimits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(result.Stdout, &response); err != nil {
		return provider.Resources{}, fmt.Errorf("decode Railway resource limits: %w", err)
	}
	var values map[string]any
	if err := json.Unmarshal(response.Data.Limits, &values); err != nil {
		return provider.Resources{}, fmt.Errorf("decode Railway resource limit value: %w", err)
	}
	number := func(values map[string]any, names ...string) float64 {
		for _, name := range names {
			if value, ok := values[name].(float64); ok {
				return value
			}
		}
		return 0
	}
	cpu := number(values, "vCPUs", "vcpus", "cpu")
	memoryMiB := int64(math.Round(number(values, "memoryGB", "memoryGb", "memory") * 1024))
	if containers, ok := values["containers"].(map[string]any); ok {
		if cpu == 0 {
			cpu = number(containers, "cpu", "vCPUs", "vcpus")
		}
		if memoryMiB == 0 {
			memoryBytes := number(containers, "memoryBytes")
			memoryMiB = int64(math.Round(memoryBytes / 1_000_000_000 * 1024))
		}
	}
	return provider.Resources{CPU: cpu, MemoryMiB: memoryMiB}, nil
}

func decodeRailwayCost(data []byte, serviceName string) provider.Cost {
	// The usage schema contains workspace/project totals plus a service list.
	// Walk it defensively because older CLI releases used different field names.
	var root any
	if json.Unmarshal(data, &root) != nil {
		return provider.Cost{Available: false, Currency: "USD", Detail: strings.TrimSpace(string(data))}
	}
	aliases := map[string]bool{serviceName: true, strings.TrimPrefix(serviceName, "vmbox-"): true}
	total := 0.0
	found := 0
	var walk func(any, string)
	walk = func(value any, inheritedName string) {
		switch typed := value.(type) {
		case []any:
			for _, child := range typed {
				walk(child, inheritedName)
			}
		case map[string]any:
			name := inheritedName
			for _, key := range []string{"serviceName", "service", "name"} {
				if text, ok := typed[key].(string); ok && text != "" {
					name = text
					break
				}
			}
			if aliases[name] {
				for _, key := range []string{"totalDollars", "cost", "totalCost", "currentCost", "amount"} {
					if amount, ok := typed[key].(float64); ok {
						total += amount
						found++
						break
					}
				}
			}
			for _, child := range typed {
				walk(child, name)
			}
		}
	}
	walk(root, "")
	if found == 0 {
		return provider.Cost{Available: false, Currency: "USD", Detail: "Railway returned no service-level cost for " + serviceName}
	}
	return provider.Cost{Available: true, Currency: "USD", Accrued: total, Detail: fmt.Sprintf("aggregated %d current/deleted Railway service entrie(s)", found)}
}

// removeLatestSuccessfulDeployment preserves Railway down semantics: remove the
// newest successful deployment, even if a newer deployment is still building.
func (p *Provider) removeLatestSuccessfulDeployment(ctx context.Context, serviceID string) error {
	items, err := p.deployments(ctx, serviceID)
	if err != nil {
		return err
	}
	var selected *railwayDeployment
	for i := range items {
		item := &items[i]
		if item.Status == "SUCCESS" && (selected == nil || item.CreatedAt.After(selected.CreatedAt)) {
			selected = item
		}
	}
	if selected == nil {
		return fmt.Errorf("Railway stop: no successful deployment found")
	}
	return p.confirmBooleanMutation(ctx, deploymentRemoveMutation, "deploymentRemove", selected.ID)
}

// A missing confirmation is ambiguous, never permission to replay a mutation.
func (p *Provider) confirmBooleanMutation(ctx context.Context, query, field, id string) error {
	result, err := p.api(ctx, query, map[string]any{"id": id})
	if err != nil {
		return err
	}
	var response struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	var confirmed bool
	if json.Unmarshal(result.Stdout, &response) != nil ||
		json.Unmarshal(response.Data[field], &confirmed) != nil || !confirmed {
		return &APIError{Status: 200, Ambiguous: true}
	}
	return nil
}
