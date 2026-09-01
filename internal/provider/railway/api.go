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
	serviceCreateMutation = `mutation($input: ServiceCreateInput!) { serviceCreate(input: $input) { id name } }`
	serviceUpdateMutation = `mutation($serviceId: String!, $environmentId: String!, $input: ServiceInstanceUpdateInput!) { serviceInstanceUpdate(serviceId: $serviceId, environmentId: $environmentId, input: $input) }`
	volumeDeleteMutation  = `mutation($volumeId: String!) { volumeDelete(volumeId: $volumeId) }`
	limitsUpdateMutation  = `mutation($input: ServiceInstanceLimitsUpdateInput!) { serviceInstanceLimitsUpdate(input: $input) }`
	limitsQuery           = `query($serviceId: String!, $environmentId: String!) { serviceInstanceLimits(serviceId: $serviceId, environmentId: $environmentId) }`
)

func (p *Provider) createService(ctx context.Context, name string) (procexec.Result, error) {
	input := map[string]any{"projectId": p.cfg.ProjectID, "environmentId": p.cfg.EnvironmentID, "name": name}
	return p.api(ctx, serviceCreateMutation, map[string]any{"input": input})
}

func (p *Provider) api(ctx context.Context, document string, variables any) (procexec.Result, error) {
	encoded, err := json.Marshal(variables)
	if err != nil {
		return procexec.Result{}, err
	}
	return p.runner.Run(ctx, []string{"railway", "api", document, "--variables", string(encoded), "--compact"}, nil, nil, nil)
}

func (p *Provider) connectImage(ctx context.Context, service, image string) error {
	result, err := p.run(ctx, "service", "source", "connect", "--service", service, "--image", image, "--json")
	if err != nil || result.ExitCode != 0 {
		return railwayError("connect image source", result, err)
	}
	return nil
}

func (p *Provider) setRegion(ctx context.Context, serviceID, region string) error {
	if region == "" {
		return nil
	}
	regions := map[string]any{"pdx": nil, "ams": nil, "sfo": nil, "iad": nil, "sin": nil}
	regions[region] = map[string]any{"numReplicas": 1}
	variables := map[string]any{"serviceId": serviceID, "environmentId": p.cfg.EnvironmentID, "input": map[string]any{"multiRegionConfig": regions}}
	result, err := p.api(ctx, serviceUpdateMutation, variables)
	if err != nil || result.ExitCode != 0 {
		return railwayError("set region", result, err)
	}
	return nil
}

func (p *Provider) setStartCommand(ctx context.Context, serviceID, command string) error {
	variables := map[string]any{"serviceId": serviceID, "environmentId": p.cfg.EnvironmentID, "input": map[string]any{"startCommand": command}}
	result, err := p.api(ctx, serviceUpdateMutation, variables)
	if err != nil || result.ExitCode != 0 {
		return railwayError("set start command", result, err)
	}
	return nil
}

func (p *Provider) deleteVolume(ctx context.Context, volumeID string) error {
	result, err := p.api(ctx, volumeDeleteMutation, map[string]any{"volumeId": volumeID})
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
