package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"

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

func parseWorkerPool(value string) (string, string, error) {
	providerName, alias, ok := strings.Cut(value, "/")
	if !ok || providerName == "" || alias == "" || strings.Contains(alias, "/") {
		return "", "", fmt.Errorf("worker pool must be TYPE/ALIAS; see vmbox pools list")
	}
	return providerName, alias, nil
}

func hasWorkerPoolOption(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--pool" || strings.HasPrefix(arg, "--pool=") {
			return true
		}
	}
	return false
}

func fleetPoolOption(args []string) ([]string, string, error) {
	result := make([]string, 0, len(args))
	pool := ""
	seen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--pool" || strings.HasPrefix(arg, "--pool=") {
			if seen {
				return nil, "", fmt.Errorf("--pool may be specified only once")
			}
			seen = true
			if arg == "--pool" {
				i++
				if i >= len(args) {
					return nil, "", fmt.Errorf("--pool requires TYPE/ALIAS")
				}
				pool = args[i]
			} else {
				pool = strings.TrimPrefix(arg, "--pool=")
			}
			if pool == "" {
				return nil, "", fmt.Errorf("--pool requires TYPE/ALIAS")
			}
			continue
		}
		result = append(result, arg)
	}
	return result, pool, nil
}

func (a *App) controllerFleet(ctx context.Context, c config.Context, token string, args []string) error {
	var pool string
	var err error
	args, pool, err = fleetPoolOption(args)
	if err != nil {
		return err
	}
	if pool != "" {
		c.Provider, c.ProviderCredential, err = parseWorkerPool(pool)
		if err != nil {
			return err
		}
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: vmbox fleet status|slots|slots set COUNT|location [set REGION] [--pool TYPE/ALIAS]")
	}
	switch args[0] {
	case "location":
		return a.controllerFleetLocation(ctx, c, token, args[1:])
	case "status":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
			return fmt.Errorf("usage: vmbox fleet status [--json] [--pool TYPE/ALIAS]")
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
			return fmt.Errorf("usage: vmbox fleet slots set COUNT [--pool TYPE/ALIAS]")
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
	fmt.Fprintf(output, "Worker pool %s", status.Provider)
	if status.ProviderCredential != "" {
		fmt.Fprintf(output, "/%s", status.ProviderCredential)
	}
	fmt.Fprintln(output)
	fmt.Fprintf(output, "desired=%d actual=%d free=%d occupied=%d starting=%d draining=%d unhealthy=%d stopped=%d pending=%d\n",
		status.DesiredSlots, status.ActualSlots, status.FreeSlots, status.OccupiedSlots,
		status.StartingSlots, status.DrainingSlots, status.UnhealthySlots, status.StoppedSlots,
		status.PendingAllocations)
	if len(status.Slots) > 0 {
		table := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "SLOT\tSTATE\tBOX\tREGION\tHEALTH\tSERVICE\tDEPLOYMENT\tIMAGE")
		for _, slot := range status.Slots {
			fmt.Fprintf(table, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", slot.Ordinal,
				fleetCell(string(slot.State), 13), fleetCell(slot.LogicalBoxName, 24),
				fleetCell(slot.Region, 20), fleetCell(slot.Health, 13),
				fleetCell(slot.ServiceID, 9), fleetCell(slot.DeploymentInstanceID, 9), fleetCell(slot.ImageVersion, 13))
		}
		table.Flush()
		for _, slot := range status.Slots {
			if slot.FailureReason != "" {
				fmt.Fprintf(output, "Slot %d: %s\n", slot.Ordinal, tuiLabel(slot.FailureReason, 140))
			}
		}
		fmt.Fprintln(output, "Full IDs and lease details: vmbox fleet status --json")
	}
	if len(status.DetachedLogicalBoxes) > 0 {
		names := make([]string, 0, len(status.DetachedLogicalBoxes))
		for _, box := range status.DetachedLogicalBoxes {
			names = append(names, box.Name+" ("+string(box.State)+")")
		}
		fmt.Fprintf(output, "detached: %s\n", strings.Join(names, ", "))
	}
}

func fleetCell(value string, width int) string {
	if value == "" {
		return "—"
	}
	return tuiLabel(value, width)
}
