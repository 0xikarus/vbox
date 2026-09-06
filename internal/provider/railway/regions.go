package railway

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"sort"
)

// Query deployment regions, not edge-network locations or existing fleet slots.
func (p *Provider) Regions(ctx context.Context) ([]provider.Region, error) {
	result, err := p.api(ctx, `query($projectId: String!) { regions(projectId: $projectId) { name country location } }`, map[string]any{"projectId": p.cfg.ProjectID})
	if err != nil || result.ExitCode != 0 {
		return nil, railwayError("list regions", result, err)
	}
	var response struct {
		Data struct {
			Regions []struct{ Name, Country, Location string }
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(result.Stdout, &response); err != nil {
		return nil, fmt.Errorf("invalid Railway regions response")
	}
	for _, issue := range response.Errors {
		if issue.Message == "Not Authorized" {
			return nil, fmt.Errorf("Railway denied region discovery; configure a provider credential with permission to query regions")
		}
	}
	if len(response.Errors) != 0 || len(response.Data.Regions) == 0 {
		return nil, fmt.Errorf("Railway did not return available regions")
	}
	regions := []provider.Region{}
	seen := map[string]bool{}
	for _, r := range response.Data.Regions {
		if r.Name == "" {
			return nil, fmt.Errorf("Railway returned a region without an identifier")
		}
		if seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		label := r.Location
		if r.Country != "" {
			if label != "" {
				label += ", "
			}
			label += r.Country
		}
		if label == "" {
			label = r.Name
		}
		regions = append(regions, provider.Region{ID: r.Name, Name: label})
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].Name < regions[j].Name })
	return regions, nil
}
