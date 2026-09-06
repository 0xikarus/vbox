package cli

import (
	"context"
	"fmt"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func (a *App) creationCapacityError(ctx context.Context, c config.Context, token string, request v1.CreateLogicalBoxRequest, cause error) error {
	c.Provider, c.ProviderCredential = request.Provider, request.ProviderCredential
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var fleet v1.FleetStatus
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/fleet/status"+fleetQuery(c), nil, &fleet, nil); err != nil {
		return fmt.Errorf("Creation paused: no free compute to initialize the workspace.\nCould not check fleet status. Run: vmbox fleet status\nYour form is retained; retry Create after a slot is free and healthy.\n%w", cause)
	}
	return fmt.Errorf("%s", creationCapacityMessage(fleet, request.Region))
}

func creationCapacityMessage(f v1.FleetStatus, region string) string {
	heading := fmt.Sprintf("Creation paused · fleet %s/%s\n", f.Provider, f.ProviderCredential)
	var advice string
	switch {
	case f.DesiredSlots == 0:
		advice = "Fleet is scaled to zero: no compute is enabled.\nRun: vmbox fleet slots set 1 (adds paid compute capacity)\nThen: vmbox fleet status; wait for a free, healthy slot."
	case f.StartingSlots > 0 || f.ActualSlots < f.DesiredSlots:
		advice = "Fleet capacity is still starting or being provisioned.\nRun: vmbox fleet status; wait for a free, healthy slot."
	case f.UnhealthySlots > 0 || f.StoppedSlots > 0:
		advice = "Fleet has unhealthy or stopped compute slots.\nRun: vmbox fleet status; inspect the affected slots before retrying."
	case region != "":
		advice = fmt.Sprintf("No free, healthy slot in selected location %s.\nChoose a location with free capacity, or free a box in this region.\nRun: vmbox fleet status", region)
	case f.OccupiedSlots > 0 || f.DrainingSlots > 0:
		advice = "Compute slots are busy or being released.\nRun: vmbox fleet status; wait for a slot to become free.\nOr hibernate an idle box: vmbox hibernate BOX (ends its live processes)."
	default:
		advice = "No healthy free slot could be reserved; capacity may have changed.\nRun: vmbox fleet status; retry when a slot is free and healthy."
	}
	return heading + advice + "\nUse that provider alias for these commands.\nKeep this form open and run commands in another terminal, then retry Create."
}
