package cli

import (
	"context"
	"fmt"
	"sort"

	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type setupRegion struct{ ID, Label string }

type clusterSource interface {
	Clusters(context.Context) ([]map[string]any, error)
}

func setupRegions(ctx context.Context, c config.Context, current string, p provider.Provider) []setupRegion {
	seen := map[string]bool{}
	var values []setupRegion
	add := func(id, label string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		if label == "" {
			label = id
		}
		values = append(values, setupRegion{ID: id, Label: label})
	}
	switch c.Provider {
	case "railway":
		add("eu-west", "EU West · Amsterdam")
		add("us-west", "US West · California")
		add("us-east", "US East · Virginia")
		add("southeast-asia", "Southeast Asia · Singapore")
		add(current, current)
	case "sevalla":
		add(c.Cluster, c.Cluster)
		if source, ok := p.(clusterSource); ok {
			if clusters, err := source.Clusters(ctx); err == nil {
				for _, cluster := range clusters {
					id, _ := cluster["id"].(string)
					label := id
					for _, key := range []string{"display_name", "name", "location"} {
						if text, ok := cluster[key].(string); ok && text != "" {
							label = fmt.Sprintf("%s · %s", id, text)
							break
						}
					}
					add(id, label)
				}
			}
		}
		add(current, current)
	default:
		add(current, current)
	}
	if current == "" && len(values) > 1 {
		sort.SliceStable(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	}
	return values
}
