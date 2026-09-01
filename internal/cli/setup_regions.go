package cli

import (
	"context"

	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type setupRegion struct{ ID, Label string }

func setupRegions(ctx context.Context, c config.Context, current string, p provider.Provider) []setupRegion {
	_ = ctx
	_ = p
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
		add("ams", "EU West · Amsterdam")
		add("sfo", "US West · California")
		add("iad", "US East · Virginia")
		add("sin", "Southeast Asia · Singapore")
		add(current, current)
	default:
		add(current, current)
	}
	return values
}
