package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Server) activateAllocation(ctx context.Context, accountID string, allocation v1.Allocation, retry bool) error {
	started := time.Now()
	fail := func(phase string, err error) error {
		_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, phase, err.Error(), retry)
		s.Logger.Error("logical box allocation phase failed", "allocation", allocation.RequestID, "phase", phase, "elapsed", time.Since(started), "error", err)
		return err
	}
	if err := s.Store.RenewAssignmentLease(ctx, accountID, allocation, 2*time.Minute); err != nil {
		return fail("renewing-lease", err)
	}
	if err := s.Store.MarkAssignmentAttaching(ctx, allocation); err != nil {
		return fail("reserving-slot", err)
	}
	assignment, err := s.Store.assignment(ctx, accountID, allocation.LogicalBoxID)
	if err != nil {
		return fail("loading-assignment", err)
	}
	if assignment.Slot.ServiceID == "" {
		return fail("loading-assignment", fmt.Errorf("reserved compute slot has no Railway service ID"))
	}
	prov, err := s.provider(ctx, accountID, assignment.Box.Provider, assignment.Box.ProviderCredential)
	if err != nil {
		return fail("resolving-provider", err)
	}
	desired := provider.Storage{ID: assignment.Box.VolumeID, Name: assignment.Box.VolumeName, MountPath: "/data"}
	if inspector, ok := prov.(provider.AttachedStorageProvider); ok {
		_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "verifying-slot", "", false)
		attached, inspectErr := inspector.AttachedStorage(ctx, assignment.Slot.ServiceID)
		if inspectErr != nil {
			return fail("verifying-slot", inspectErr)
		}
		if attached != nil && attached.ID != desired.ID {
			return fail("verifying-slot", fmt.Errorf("compute slot %s unexpectedly contains volume %s", assignment.Slot.ID, attached.ID))
		}
		if attached == nil {
			_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "attaching-volume", "", false)
			if err := prov.AttachStorage(ctx, assignment.Slot.ServiceID, desired); err != nil {
				return fail("attaching-volume", err)
			}
		}
	} else {
		_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "attaching-volume", "", false)
		if err := prov.AttachStorage(ctx, assignment.Slot.ServiceID, desired); err != nil {
			return fail("attaching-volume", err)
		}
	}
	_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "waiting-for-runtime", "", false)
	if err := stageWorkspaceRuntime(ctx, prov, assignment.Slot.ServiceID, s.WorkerRuntime); err != nil {
		return fail("waiting-for-runtime", err)
	}
	health, err := prov.Exec(ctx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "health"}, provider.ExecOptions{})
	if err != nil {
		return fail("waiting-for-runtime", err)
	}
	if health.ExitCode != 0 || strings.TrimSpace(health.Stdout) != "ok" {
		return fail("waiting-for-runtime", fmt.Errorf("runtime health check failed with status %d: %s", health.ExitCode, strings.TrimSpace(health.Stderr)))
	}
	_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "resolving-ssh", "", false)
	connection, err := prov.Connection(ctx, assignment.Slot.ServiceID)
	if err != nil {
		return fail("resolving-ssh", err)
	}
	_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "restoring-tmux", "", false)
	restored, err := prov.Exec(ctx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "tmux-restore"}, provider.ExecOptions{})
	if err != nil {
		return fail("restoring-tmux", err)
	}
	if restored.ExitCode != 0 {
		return fail("restoring-tmux", fmt.Errorf("tmux restoration exited with status %d: %s", restored.ExitCode, strings.TrimSpace(restored.Stderr)))
	}
	guide, err := prov.Exec(ctx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "tmux-context", assignment.Box.Name, assignment.Slot.ServiceName, "running", "connected"}, provider.ExecOptions{})
	if err != nil {
		return fail("restoring-tmux-guide", err)
	}
	if guide.ExitCode != 0 {
		return fail("restoring-tmux-guide", fmt.Errorf("tmux guide update exited with status %d: %s", guide.ExitCode, strings.TrimSpace(guide.Stderr)))
	}
	deploymentID := connection.Metadata["deploymentInstanceId"]
	if err := s.Store.CompleteAssignment(ctx, accountID, assignment.Box.ID, allocation.AssignmentGeneration, allocation.FencingToken, deploymentID); err != nil {
		return fail("marking-ready", err)
	}
	_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "ready", "", false)
	s.Logger.Info("logical box allocation ready", "allocation", allocation.RequestID, "box", assignment.Box.Name, "slot", assignment.Slot.Ordinal, "elapsed", time.Since(started))
	return nil
}

func (s *Server) ReconcileAllocationsNow(ctx context.Context) error {
	values, err := s.Store.RecoverableAllocations(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, value := range values {
		if err := s.activateAllocation(ctx, value.AccountID, value.Allocation, true); err != nil {
			failures = append(failures, fmt.Errorf("allocation %s: %w", value.Allocation.RequestID, err))
		}
	}
	configs, configErr := s.Store.ListFleetConfigs(ctx)
	if configErr != nil {
		failures = append(failures, configErr)
	} else {
		for _, config := range configs {
			for {
				allocation, reserved, reserveErr := s.Store.ReserveNextQueuedAllocation(ctx, config.AccountID, config.Config.Provider, config.Config.ProviderCredential)
				if reserveErr != nil {
					failures = append(failures, reserveErr)
					break
				}
				if !reserved {
					break
				}
				if err := s.activateAllocation(ctx, config.AccountID, allocation, false); err != nil {
					failures = append(failures, fmt.Errorf("queued allocation %s: %w", allocation.RequestID, err))
					break
				}
			}
		}
	}
	return errors.Join(failures...)
}

func (s *Server) hibernateLogicalBox(ctx context.Context, p Principal, id string) (v1.LogicalBox, error) {
	assignment, err := s.Store.BeginLogicalBoxRelease(ctx, p, id, v1.LogicalBoxHibernating)
	if err != nil {
		return assignment.Box, err
	}
	if assignment.Released {
		return assignment.Box, nil
	}
	fail := func(err error) (v1.LogicalBox, error) {
		_ = s.Store.RecordReleaseFailure(ctx, p.AccountID, assignment, err.Error())
		return assignment.Box, err
	}
	prov, err := s.provider(ctx, p.AccountID, assignment.Box.Provider, assignment.Box.ProviderCredential)
	if err != nil {
		return fail(err)
	}
	detachable, ok := prov.(provider.DetachableStorageProvider)
	if !ok {
		return fail(fmt.Errorf("provider %s does not support detachable workspace volumes", prov.Name()))
	}
	prepared, err := prov.Exec(ctx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "prepare-hibernate"}, provider.ExecOptions{})
	if err != nil {
		return fail(fmt.Errorf("save workload state: %w", err))
	}
	if prepared.ExitCode != 0 {
		return fail(fmt.Errorf("workspace remained busy; volume is still attached: %s", strings.TrimSpace(prepared.Stderr)))
	}
	storage := provider.Storage{ID: assignment.Box.VolumeID, Name: assignment.Box.VolumeName, MountPath: "/data"}
	if err := detachable.DetachStorage(ctx, assignment.Slot.ServiceID, storage); err != nil {
		return fail(fmt.Errorf("detach retained volume: %w", err))
	}
	if inspector, ok := prov.(provider.AttachedStorageProvider); ok {
		attached, err := inspector.AttachedStorage(ctx, assignment.Slot.ServiceID)
		if err != nil {
			return fail(fmt.Errorf("verify detached volume: %w", err))
		}
		if attached != nil {
			return fail(fmt.Errorf("Railway still reports volume %s attached; slot remains draining", attached.ID))
		}
	}
	if err := detachable.SanitizeSlot(ctx, assignment.Slot.ServiceID); err != nil {
		return fail(fmt.Errorf("start clean idle deployment: %w", err))
	}
	if err := s.Store.ReleaseAssignment(ctx, p.AccountID, assignment.Box.ID, assignment.Box.AssignmentGeneration, assignment.FencingToken, v1.LogicalBoxHibernated); err != nil {
		return fail(err)
	}
	assignment.Box.State = v1.LogicalBoxHibernated
	assignment.Box.SlotID = ""
	return assignment.Box, nil
}

func (s *Server) deleteLogicalBoxVolume(ctx context.Context, p Principal, id, confirmation string) error {
	box, err := s.Store.LogicalBox(ctx, p, id)
	if err != nil {
		return err
	}
	if confirmation == "" || confirmation != box.Name {
		return fmt.Errorf("deletion confirmation must exactly match logical box name %q", box.Name)
	}
	assignment, err := s.Store.BeginLogicalBoxRelease(ctx, p, id, v1.LogicalBoxDeleting)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = s.Store.RecordReleaseFailure(ctx, p.AccountID, assignment, err.Error())
		return err
	}
	prov, err := s.provider(ctx, p.AccountID, assignment.Box.Provider, assignment.Box.ProviderCredential)
	if err != nil {
		return fail(err)
	}
	detachable, ok := prov.(provider.DetachableStorageProvider)
	if assignment.Slot.ID != "" && !ok {
		return fail(fmt.Errorf("provider %s does not support safe volume detachment", prov.Name()))
	}
	storage := provider.Storage{ID: assignment.Box.VolumeID, Name: assignment.Box.VolumeName, MountPath: "/data"}
	if assignment.Slot.ID != "" {
		prepared, err := prov.Exec(ctx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "prepare-hibernate"}, provider.ExecOptions{})
		if err != nil {
			return fail(fmt.Errorf("stop workload and flush volume: %w", err))
		}
		if prepared.ExitCode != 0 {
			return fail(fmt.Errorf("workspace remained busy; deletion stopped before detachment: %s", strings.TrimSpace(prepared.Stderr)))
		}
		if err := detachable.DetachStorage(ctx, assignment.Slot.ServiceID, storage); err != nil {
			return fail(fmt.Errorf("detach before deletion: %w", err))
		}
	}
	if err := prov.DeleteStorage(ctx, storage, provider.Owner{AccountID: p.AccountID, BoxID: assignment.Box.Name}); err != nil {
		return fail(fmt.Errorf("delete exact confirmed volume %s: %w", storage.ID, err))
	}
	if assignment.Slot.ID != "" {
		if err := detachable.SanitizeSlot(ctx, assignment.Slot.ServiceID); err != nil {
			return fail(fmt.Errorf("volume is deleted but slot cleanup needs recovery: %w", err))
		}
	}
	return s.Store.DeleteLogicalBoxRecord(ctx, p, assignment)
}
