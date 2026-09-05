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
	add(current, current)
	return values
}
