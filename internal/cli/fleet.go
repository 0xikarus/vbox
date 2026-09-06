package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func parseFleetSlotCount(text string, providerName, credential string) (v1.FleetConfig, error) {
	count, err := strconv.Atoi(text)
	if err != nil {
		return v1.FleetConfig{}, fmt.Errorf("compute slot count must be an integer: %w", err)
	}
	config := v1.FleetConfig{Provider: providerName, ProviderCredential: credential, ComputeBoxSlots: count}
	return config, config.Validate()
}

func fleetQuery(c config.Context) string {
	return "?provider=" + url.QueryEscape(c.Provider) + "&providerCredential=" + url.QueryEscape(c.ProviderCredential)
}

func (a *App) controllerFleet(ctx context.Context, c config.Context, token string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vmbox fleet status|slots|slots set COUNT|location [set REGION]")
	}
	switch args[0] {
	case "location":
		return a.controllerFleetLocation(ctx, c, token, args[1:])
	case "status":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
			return fmt.Errorf("usage: vmbox fleet status [--json]")
		}
		var status v1.FleetStatus
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/fleet/status"+fleetQuery(c), nil, &status, nil); err != nil {
			return err
		}
		if len(args) == 2 {
			return json.NewEncoder(a.Out).Encode(status)
		}
		writeFleetStatus(a.Out, status)
		return nil
	case "slots":
		if len(args) == 1 {
			var current v1.FleetConfig
			if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/fleet/slots"+fleetQuery(c), nil, &current, nil); err != nil {
				return err
			}
			fmt.Fprintln(a.Out, current.ComputeBoxSlots)
			return nil
		}
		if len(args) != 3 || args[1] != "set" {
			return fmt.Errorf("usage: vmbox fleet slots set COUNT")
		}
		requested, err := parseFleetSlotCount(args[2], c.Provider, c.ProviderCredential)
		if err != nil {
			return err
		}
		var current v1.FleetConfig
		body := v1.SetFleetSlotsRequest{Provider: requested.Provider, ProviderCredential: requested.ProviderCredential, ComputeBoxSlots: requested.ComputeBoxSlots}
		if _, err := a.request(ctx, c, token, http.MethodPut, "/v1/fleet/slots", body, &current, nil); err != nil {
			return err
		}
		fmt.Fprintf(a.Out, "compute_box_slots = %d\n", current.ComputeBoxSlots)
		return nil
	default:
		return fmt.Errorf("unknown fleet command %q", args[0])
	}
}

func writeFleetStatus(output interface{ Write([]byte) (int, error) }, status v1.FleetStatus) {
	fmt.Fprintf(output, "Fleet %s", status.Provider)
	if status.ProviderCredential != "" {
		fmt.Fprintf(output, "/%s", status.ProviderCredential)
	}
	fmt.Fprintln(output)
	fmt.Fprintf(output, "desired=%d actual=%d free=%d occupied=%d starting=%d draining=%d unhealthy=%d stopped=%d pending=%d\n",
		status.DesiredSlots, status.ActualSlots, status.FreeSlots, status.OccupiedSlots,
		status.StartingSlots, status.DrainingSlots, status.UnhealthySlots, status.StoppedSlots,
		status.PendingAllocations)
	if len(status.Slots) > 0 {
		fmt.Fprintln(output, "SLOT  STATE      BOX                 SERVICE              DEPLOYMENT           REGION  IMAGE VERSION        HEALTH")
		for _, slot := range status.Slots {
			deployment := slot.DeploymentInstanceID
			if len(deployment) > 20 {
				deployment = deployment[:20]
			}
			fmt.Fprintf(output, "%-5d %-10s %-19s %-20s %-20s %-7s %-20s %s\n", slot.Ordinal, slot.State, slot.LogicalBoxName, slot.ServiceID, deployment, slot.Region, slot.ImageVersion, slot.Health)
			if slot.LeaseOwner != "" {
				expires := "none"
				if slot.LeaseExpiresAt != nil {
					expires = slot.LeaseExpiresAt.Format("2006-01-02T15:04:05Z07:00")
				}
				fmt.Fprintf(output, "      lease=%s expires=%s\n", slot.LeaseOwner, expires)
			}
		}
	}
	if len(status.DetachedLogicalBoxes) > 0 {
		names := make([]string, 0, len(status.DetachedLogicalBoxes))
		for _, box := range status.DetachedLogicalBoxes {
			names = append(names, box.Name+" ("+string(box.State)+")")
		}
		fmt.Fprintf(output, "detached: %s\n", strings.Join(names, ", "))
	}
}
