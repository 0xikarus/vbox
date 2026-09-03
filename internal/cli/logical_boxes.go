package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func (a *App) controllerBoxes(ctx context.Context, c config.Context, token string, args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list", "ls":
		jsonOutput := len(args) == 2 && args[1] == "--json"
		if len(args) > 2 || (len(args) == 2 && !jsonOutput) {
			return fmt.Errorf("usage: vmbox boxes [list] [--json]")
		}
		var boxes []v1.LogicalBox
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes"+fleetQuery(c), nil, &boxes, nil); err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(a.Out).Encode(boxes)
		}
		fmt.Fprintln(a.Out, "NAME                 STATE        SLOT                 VOLUME ID            FAILURE")
		for _, box := range boxes {
			volumeID := box.VolumeID
			if len(volumeID) > 20 {
				volumeID = volumeID[:20]
			}
			fmt.Fprintf(a.Out, "%-20s %-12s %-20s %-20s %s\n", box.Name, box.State, box.SlotID, volumeID, box.FailureReason)
		}
		return nil
	case "status":
		if len(args) != 2 {
			return fmt.Errorf("usage: vmbox boxes status NAME")
		}
		box, err := a.controllerLogicalBox(ctx, c, token, args[1])
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(box)
	case "create", "new":
		if len(args) < 2 {
			return fmt.Errorf("usage: vmbox new NAME [--disk GiB] [--region ID] [--allocate]")
		}
		fs := flag.NewFlagSet("new", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		disk := fs.Int64("disk", 10, "persistent workspace size in GiB")
		region := fs.String("region", "", "preferred region")
		allocate := fs.Bool("allocate", false, "allocate a warm slot after volume creation")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("unexpected creation argument %q", fs.Arg(0))
		}
		request := v1.CreateLogicalBoxRequest{Name: args[1], Provider: c.Provider, ProviderCredential: c.ProviderCredential, Region: *region, DiskGiB: *disk, AllocateWhenReady: *allocate, AllocationRequestKey: "cli-create:" + args[1] + ":" + fmt.Sprint(time.Now().UnixNano())}
		var box v1.LogicalBox
		status, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes", request, &box, map[string]string{"Idempotency-Key": request.AllocationRequestKey})
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Err, "vmbox: logical box %q accepted (%d); initializing its persistent volume on a fenced warm slot\n", box.Name, status)
		box, err = a.waitLogicalBoxCreation(ctx, c, token, box)
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(box)
	case "allocate", "open":
		if len(args) != 2 {
			return fmt.Errorf("usage: vmbox boxes allocate NAME")
		}
		key := "cli-allocate:" + args[1] + ":" + fmt.Sprint(time.Now().UnixNano())
		var allocation v1.Allocation
		_, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(args[1])+"/allocate", map[string]any{"leaseOwner": "cli"}, &allocation, map[string]string{"Idempotency-Key": key})
		if err != nil {
			return err
		}
		allocation, err = a.waitAllocation(ctx, c, token, allocation)
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(allocation)
	case "hibernate":
		if len(args) != 2 {
			return fmt.Errorf("usage: vmbox hibernate NAME")
		}
		var box v1.LogicalBox
		if _, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(args[1])+"/hibernate", map[string]any{}, &box, nil); err != nil {
			return err
		}
		fmt.Fprintf(a.Err, "vmbox: hibernated %q; volume %s (%s) was retained and its compute slot was freed\n", box.Name, box.VolumeName, box.VolumeID)
		return nil
	case "delete-volume":
		if len(args) != 2 {
			return fmt.Errorf("usage: vmbox delete-volume NAME")
		}
		box, err := a.controllerLogicalBox(ctx, c, token, args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Err, "\nPERMANENT DELETION: logical box %q\nRailway volume: %s (ID: %s)\nAll workspace data will be permanently deleted. The fleet service will remain.\nType %s to confirm: ", box.Name, box.VolumeName, box.VolumeID, box.Name)
		timeout := a.ExitPromptTimeout
		if timeout <= 0 {
			timeout = defaultExitPromptTimeout
		}
		confirmation, ok := readLineWithTimeout(ctx, bufio.NewReader(a.In), timeout)
		if !ok || strings.TrimSpace(confirmation) != box.Name {
			fmt.Fprintln(a.Err, "vmbox: deletion cancelled; logical box and volume were kept")
			return nil
		}
		if _, err := a.request(ctx, c, token, http.MethodDelete, "/v1/logical-boxes/"+url.PathEscape(box.ID)+"/volume", map[string]string{"confirmation": box.Name}, nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(a.Err, "vmbox: deleted only volume %s (%s); compute fleet size is unchanged\n", box.VolumeName, box.VolumeID)
		return nil
	default:
		return fmt.Errorf("unknown boxes command %q", args[0])
	}
}

func (a *App) controllerLogicalBox(ctx context.Context, c config.Context, token, id string) (v1.LogicalBox, error) {
	var box v1.LogicalBox
	_, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes/"+url.PathEscape(id), nil, &box, nil)
	return box, err
}

func (a *App) waitLogicalBoxCreation(ctx context.Context, c config.Context, token string, box v1.LogicalBox) (v1.LogicalBox, error) {
	lastPhase := ""
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if box.RestorationState != lastPhase {
			fmt.Fprintf(a.Err, "vmbox: phase=%s state=%s\n", box.RestorationState, box.State)
			lastPhase = box.RestorationState
		}
		switch box.State {
		case v1.LogicalBoxHibernated, v1.LogicalBoxDetached, v1.LogicalBoxRunning:
			return box, nil
		case v1.LogicalBoxFailed:
			return box, fmt.Errorf("logical box creation failed: %s", box.FailureReason)
		}
		select {
		case <-ctx.Done():
			return box, ctx.Err()
		case <-ticker.C:
			var err error
			box, err = a.controllerLogicalBox(ctx, c, token, box.ID)
			if err != nil {
				return box, err
			}
		}
	}
}

func (a *App) waitAllocation(ctx context.Context, c config.Context, token string, allocation v1.Allocation) (v1.Allocation, error) {
	last := ""
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		message := allocation.Phase
		if allocation.State == "queued" {
			message = fmt.Sprintf("waiting-for-capacity queue=%d", allocation.QueuePosition)
		}
		if message != last {
			fmt.Fprintf(a.Err, "vmbox: allocation=%s phase=%s retry=%d\n", allocation.RequestID, message, allocation.RetryCount)
			last = message
		}
		switch allocation.State {
		case "ready":
			return allocation, nil
		case "failed", "cancelled":
			return allocation, fmt.Errorf("allocation %s: %s", allocation.State, allocation.FailureReason)
		}
		select {
		case <-ctx.Done():
			return allocation, ctx.Err()
		case <-ticker.C:
			if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/allocations/"+url.PathEscape(allocation.RequestID), nil, &allocation, nil); err != nil {
				return allocation, err
			}
		}
	}
}
