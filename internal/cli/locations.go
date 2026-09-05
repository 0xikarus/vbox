package cli

import (
	"context"
	"fmt"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func (a *App) pickLocation(ctx context.Context, c config.Context, token string) (string, error) {
	var locations []v1.LocationPreset
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/locations"+fleetQuery(c), nil, &locations, nil); err != nil {
		return "", err
	}
	labels := []string{"Any available fleet location"}
	initial := 0
	key := c.Provider + "/" + c.ProviderCredential
	for i, location := range locations {
		labels = append(labels, fmt.Sprintf("%s (%d free slots)", location.ID, location.AvailableSlots))
		if c.LocationPresets[key] == location.ID {
			initial = i + 1
		}
	}
	i, err := a.selectTUI(ctx, "Location preset", labels, initial)
	if err != nil {
		return "", err
	}
	region := ""
	if i > 0 {
		region = locations[i-1].ID
	}
	file, err := config.Load(a.ConfigPath)
	if err != nil {
		return "", err
	}
	if stored, ok := file.Contexts[c.Name]; ok && stored.Controller == c.Controller {
		if stored.LocationPresets == nil {
			stored.LocationPresets = map[string]string{}
		}
		stored.LocationPresets[key] = region
		file.Contexts[c.Name] = stored
		if err := config.Save(a.ConfigPath, file); err != nil {
			return "", err
		}
	}
	return region, nil
}
