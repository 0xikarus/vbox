package cli

import (
	"context"
	"fmt"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (a *App) controllerFleetLocation(ctx context.Context, c config.Context, token string, args []string) error {
	if len(args) != 0 && !(len(args) == 2 && args[0] == "set") {
		return fmt.Errorf("usage: vmbox fleet location [set REGION] [--pool TYPE/ALIAS]")
	}
	region := ""
	if len(args) == 2 {
		region = args[1]
	} else {
		var current v1.FleetConfig
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/fleet/slots"+fleetQuery(c), nil, &current, nil); err != nil {
			return err
		}
		var regions []provider.Region
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/fleet/regions"+fleetQuery(c), nil, &regions, nil); err != nil {
			return err
		}
		if len(regions) == 0 {
			return fmt.Errorf("worker pool returned no locations")
		}
		labels := make([]string, len(regions))
		initial := 0
		for i, r := range regions {
			labels[i] = r.Name + " (" + r.ID + ")"
			if r.ID == current.Region {
				initial = i
			}
		}
		index, err := a.selectTUI(ctx, "Fleet location (empty, scaled-down fleet only)", labels, initial)
		if err != nil {
			return err
		}
		region = regions[index].ID
	}
	body := map[string]string{"provider": c.Provider, "providerCredential": c.ProviderCredential, "region": region}
	if _, err := a.request(ctx, c, token, http.MethodPut, "/v1/fleet/location", body, nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "Fleet location: %s\nScale up with: vmbox fleet slots set COUNT\n", region)
	return nil
}
