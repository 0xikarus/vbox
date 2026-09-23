package controller

import (
	"bytes"
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
	if s.DirectWorkersEnabled && assignment.Box.Provider == "railway" {
		_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "reconciling-worker-agent", "", false)
		if err := s.ensureReplacementWorkerTransport(ctx, accountID, assignment, prov); err != nil {
			return fail("reconciling-worker-agent", err)
		}
		bootstrapConnection, err := prov.Connection(ctx, assignment.Slot.ServiceID)
		if err != nil {
			return fail("resolving-worker-bootstrap", err)
		}
		_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "enrolling-worker-agent", "", false)
		if err := s.ensureAutomaticWorkerTransport(ctx, accountID, assignment, prov, bootstrapConnection); err != nil {
			return fail("enrolling-worker-agent", err)
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
	// System packages belong to replaceable compute. The retained recipe rebuilds
	// them before declaring an allocation ready; a failed install blocks startup.
	if err := s.Store.RenewAssignmentLease(ctx, accountID, allocation, 8*time.Minute); err != nil {
		return fail("restoring-tools", err)
	}
	// A queued credential edit applies here, while the volume is attached and
	// before the box is declared ready, so a stopped box only needs a resume.
	_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "restoring-credentials", "", false)
	if err := s.provisionPendingBoxProfiles(ctx, prov, accountID, assignment); err != nil {
		return fail("restoring-credentials", err)
	}
	// The box's instruction snapshot follows attachment like the retained tool
	// recipe: hibernation resume, replacement, and queued recovery all funnel
	// here, and the box-side reconcile is idempotent on identical content.
	_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "syncing-instructions", "", false)
	if err := s.syncBoxInstructions(ctx, prov, accountID, allocation.LogicalBoxID, assignment.Slot.ServiceID); err != nil {
		return fail("syncing-instructions", err)
	}
	_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "restoring-tools", "", false)
	toolCtx, toolCancel := context.WithTimeout(ctx, 6*time.Minute)
	toolResult, toolErr := prov.Exec(toolCtx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "restore-tools"}, provider.ExecOptions{})
	toolCancel()
	if toolErr != nil {
		return fail("restoring-tools", toolErr)
	}
	if toolResult.ExitCode != 0 {
		return fail("restoring-tools", fmt.Errorf("custom tool restoration failed: %s", strings.TrimSpace(toolResult.Stderr)))
	}
	_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "restoring-workspace-metadata", "", false)
	actual, inspectErr := prov.Inspect(ctx, assignment.Slot.ServiceID)
	if inspectErr != nil {
		return fail("restoring-workspace-metadata", fmt.Errorf("inspect allocated compute: %w", inspectErr))
	}
	welcome := logicalBoxWelcome(assignment, actual)
	written, err := prov.Exec(ctx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "put-file", "/data/home/.vmbox-welcome", "0600"}, provider.ExecOptions{Stdin: bytes.NewReader(welcome)})
	if err != nil {
		return fail("restoring-workspace-metadata", fmt.Errorf("write logical-box welcome: %w", err))
	}
	if written.ExitCode != 0 {
		return fail("restoring-workspace-metadata", fmt.Errorf("write logical-box welcome exited with status %d: %s", written.ExitCode, strings.TrimSpace(written.Stderr)))
	}
	_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "resolving-ssh", "", false)
	connection, err := prov.Connection(ctx, assignment.Slot.ServiceID)
	if err != nil {
		return fail("resolving-ssh", err)
	}
	deploymentID := connection.Metadata["deploymentInstanceId"]
	if connection.Transport == directWorkerTransport {
		workerState, stateErr := s.Store.workerEnrollmentForSlot(ctx, accountID, assignment.Slot.ID)
		if stateErr != nil || !workerState.Worker.Enabled || !workerState.Live || workerState.BootstrapDeployment == "" {
			return fail("resolving-ssh", errors.New("direct worker deployment identity unavailable"))
		}
		deploymentID = workerState.BootstrapDeployment
	}
	_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "restoring-tmux", "", false)
	// Restored managed agents start desktop and native helpers immediately.
	// Their assignment must exist before tmux launches those processes.
	bound, bindErr := prov.Exec(ctx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "native-bind", nativeFence(assignment)}, provider.ExecOptions{})
	if bindErr != nil || bound.ExitCode != 0 {
		return fail("binding-native-sessions", fmt.Errorf("worker could not bind native session assignment"))
	}
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
	if err := s.Store.CompleteAssignment(ctx, accountID, assignment.Box.ID, allocation.AssignmentGeneration, allocation.FencingToken, deploymentID); err != nil {
		return fail("marking-ready", err)
	}
	_ = s.Store.UpdateAllocationProgress(ctx, accountID, allocation.RequestID, "ready", "", false)
	s.Logger.Info("logical box allocation ready", "allocation", allocation.RequestID, "box", assignment.Box.Name, "slot", assignment.Slot.Ordinal, "elapsed", time.Since(started))
	return nil
}

func logicalBoxWelcome(assignment fleetAssignment, actual provider.Box) []byte {
	region := actual.Region
	if region == "" {
		region = assignment.Slot.Region
	}
	if region == "" {
		region = "provider default"
	}
	cpu := "fleet default"
	if actual.Resources.CPU > 0 {
		cpu = fmt.Sprintf("%.2g CPU", actual.Resources.CPU)
	}
	memory := "fleet default"
	if actual.Resources.MemoryMiB > 0 {
		memory = fmt.Sprintf("%d MiB RAM", actual.Resources.MemoryMiB)
	}
	disk := "persistent volume"
	size := actual.Resources.DiskGiB
	if actual.Storage != nil && actual.Storage.SizeGiB > 0 {
		size = actual.Storage.SizeGiB
	}
	if size > 0 {
		disk = fmt.Sprintf("%d GiB disk", size)
	}
	return []byte(fmt.Sprintf("vmbox %s is ready\nProvider: %s (controller)  Region: %s\nSpecs: %s / %s / %s\nWorkspace: /data/workspace  Volume: %s\nCompute slot: %s  State: running\nConnection: direct OpenSSH, resolved for this deployment\nCost: managed fleet slot; see provider billing\nDetach safely: press Ctrl-a, release both keys, then press d\nUseful: vmbox %s | vmbox hibernate %s\n\n",
		assignment.Box.Name, assignment.Box.Provider, region, cpu, memory, disk,
		assignment.Box.VolumeName, assignment.Slot.ServiceName, assignment.Box.Name, assignment.Box.Name))
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

func (s *Server) startLogicalBoxHibernate(p Principal, id string) {
	run := s.resumeLogicalBoxHibernate
	if s.StartHibernate != nil {
		run = s.StartHibernate
	}
	go func() {
		if err := run(context.Background(), p, id); err != nil {
			s.Logger.Warn("logical box hibernate attempt stopped", "box", id, "error", err)
		}
	}()
}

func (s *Server) ReconcileLogicalBoxHibernatesNow(ctx context.Context) error {
	values, err := s.Store.PendingLogicalBoxHibernates(ctx)
	if err != nil {
		return err
	}
	for _, value := range values {
		p := Principal{AccountID: value.AccountID, UserID: value.OwnerUserID, Role: "user", Subject: "controller:hibernate-reconciler"}
		s.startLogicalBoxHibernate(p, value.BoxID)
	}
	return nil
}

func (s *Server) resumeLogicalBoxHibernate(ctx context.Context, p Principal, id string) error {
	assignment, err := s.Store.BeginLogicalBoxRelease(ctx, p, id, v1.LogicalBoxHibernating)
	if err != nil {
		return err
	}
	if assignment.Released {
		return nil
	}
	claim, claimed, err := s.Store.ClaimLogicalBoxHibernate(ctx, p.AccountID, assignment.Box.ID)
	if err != nil || !claimed {
		return err
	}
	// Claims preserve automatic intent for releases started by the controller.
	// Use the durable value, not the pre-claim snapshot.
	box, err := s.Store.LogicalBox(ctx, p, assignment.Box.ID)
	if err != nil {
		_ = s.Store.ReleaseLogicalBoxHibernateClaim(ctx, p.AccountID, assignment.Box.ID, claim)
		return err
	}
	assignment.Box.RestorationState = box.RestorationState
	operationCtx, cancel := context.WithCancel(ctx)
	heartbeatDone := make(chan error, 1)
	go s.heartbeatLogicalBoxHibernate(operationCtx, cancel, p.AccountID, assignment.Box.ID, claim, heartbeatDone)
	_, operationErr := s.completeLogicalBoxHibernate(operationCtx, p, assignment, claim)
	cancel()
	heartbeatErr := <-heartbeatDone
	if operationErr == nil {
		return nil
	}
	if heartbeatErr != nil && !errors.Is(heartbeatErr, context.Canceled) {
		operationErr = errors.Join(operationErr, heartbeatErr)
	}
	settleCtx, settleCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer settleCancel()
	_ = s.Store.RecordReleaseFailure(settleCtx, p.AccountID, assignment, operationErr.Error())
	_ = s.Store.ReleaseLogicalBoxHibernateClaim(settleCtx, p.AccountID, assignment.Box.ID, claim)
	return operationErr
}

func (s *Server) heartbeatLogicalBoxHibernate(ctx context.Context, cancel context.CancelFunc, accountID, boxID, claim string, done chan<- error) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			renewed, err := s.Store.RenewLogicalBoxHibernate(ctx, accountID, boxID, claim)
			if err != nil || !renewed {
				if err == nil {
					err = fmt.Errorf("logical-box hibernate claim was lost")
				}
				cancel()
				done <- err
				return
			}
		}
	}
}

func (s *Server) completeLogicalBoxHibernate(ctx context.Context, p Principal, assignment fleetAssignment, claim string) (v1.LogicalBox, error) {
	fail := func(err error) (v1.LogicalBox, error) { return assignment.Box, err }
	prov, err := s.provider(ctx, p.AccountID, assignment.Box.Provider, assignment.Box.ProviderCredential)
	if err != nil {
		return fail(err)
	}
	detachable, ok := prov.(provider.DetachableStorageProvider)
	if !ok {
		return fail(fmt.Errorf("provider %s does not support detachable workspace volumes", prov.Name()))
	}
	if err := s.Store.SetLogicalBoxHibernatePhase(ctx, p.AccountID, assignment.Box.ID, claim, "saving-workspace"); err != nil {
		return fail(err)
	}
	storage := provider.Storage{ID: assignment.Box.VolumeID, Name: assignment.Box.VolumeName, MountPath: "/data"}
	inspector, canInspect := prov.(provider.AttachedStorageProvider)
	alreadyDetached := false
	if canInspect {
		attached, err := inspector.AttachedStorage(ctx, assignment.Slot.ServiceID)
		if err != nil {
			return fail(fmt.Errorf("inspect hibernate volume: %w", err))
		}
		if attached == nil {
			// A previous attempt can detach and sanitize successfully, then lose
			// its final serializable database transaction. Retrying commands on
			// that empty slot can never work; finish the durable release instead.
			alreadyDetached = true
		} else if attached.ID != storage.ID {
			return fail(fmt.Errorf("compute slot unexpectedly contains volume %s while hibernating %s", attached.ID, storage.ID))
		}
	}
	if !alreadyDetached {
		// A running volume may still have the runtime from an earlier controller
		// revision. Apply snapshot fixes before saving, without restarting compute.
		if err := stageWorkspaceRuntime(ctx, prov, assignment.Slot.ServiceID, s.WorkerRuntime); err != nil {
			return fail(fmt.Errorf("stage hibernate runtime: %w", err))
		}
		prepareCommand := "prepare-hibernate"
		if strings.HasPrefix(assignment.Box.RestorationState, "auto-") {
			prepareCommand = "prepare-idle-hibernate"
		}
		flushCtx, flushCancel := context.WithTimeout(ctx, 2*time.Minute)
		prepared, err := prov.Exec(flushCtx, assignment.Slot.ServiceID, []string{"vmbox-runtime", prepareCommand}, provider.ExecOptions{})
		flushCancel()
		if err != nil {
			return fail(fmt.Errorf("save workload state: %w", err))
		}
		if prepared.ExitCode != 0 {
			return fail(fmt.Errorf("workspace remained busy; volume is still attached: %s", strings.TrimSpace(prepared.Stderr)))
		}
		if err := s.Store.SetLogicalBoxHibernatePhase(ctx, p.AccountID, assignment.Box.ID, claim, "detaching-volume"); err != nil {
			return fail(err)
		}
		if err := detachable.DetachStorage(ctx, assignment.Slot.ServiceID, storage); err != nil {
			return fail(fmt.Errorf("detach retained volume: %w", err))
		}
		if canInspect {
			if err := s.Store.SetLogicalBoxHibernatePhase(ctx, p.AccountID, assignment.Box.ID, claim, "verifying-detach"); err != nil {
				return fail(err)
			}
			attached, err := inspector.AttachedStorage(ctx, assignment.Slot.ServiceID)
			if err != nil {
				return fail(fmt.Errorf("verify detached volume: %w", err))
			}
			if attached != nil {
				return fail(fmt.Errorf("Railway still reports volume %s attached; slot remains draining", attached.ID))
			}
		}
	}
	if err := s.Store.SetLogicalBoxHibernatePhase(ctx, p.AccountID, assignment.Box.ID, claim, "sanitizing-compute"); err != nil {
		return fail(err)
	}
	if err := detachable.SanitizeSlot(ctx, assignment.Slot.ServiceID); err != nil {
		return fail(fmt.Errorf("start clean idle deployment: %w", err))
	}
	if err := s.Store.ReleaseAssignment(ctx, p.AccountID, assignment.Box.ID, assignment.Box.AssignmentGeneration, assignment.FencingToken, v1.LogicalBoxHibernated); err != nil {
		return fail(err)
	}
	return s.Store.LogicalBox(ctx, p, assignment.Box.ID)
}
