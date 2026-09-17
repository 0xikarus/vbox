package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func pendingVolume(id string) bool { return strings.HasPrefix(id, "pending:") }

const stagedRuntimePath = "/data/home/bin/.vmbox-runtime-staged"
const workspaceRuntimePath = "/data/home/bin/vmbox-runtime"

// stageWorkspaceRuntime keeps a retained volume on the same runtime revision
// as its controller. The audited base image remains immutable; the current,
// credential-free binary is streamed with mode 0600 over the selected transport, then verified and
// atomically installed by the unprivileged workload owner with mode 0700.
func stageWorkspaceRuntime(ctx context.Context, prov provider.Provider, serviceID string, runtime []byte) error {
	return stageWorkspaceRuntimeWithExec(ctx, runtime, func(ctx context.Context, argv []string, options provider.ExecOptions) (provider.ExecResult, error) {
		return prov.Exec(ctx, serviceID, argv, options)
	})
}

func stageWorkspaceRuntimeWithExec(ctx context.Context, runtime []byte, execute func(context.Context, []string, provider.ExecOptions) (provider.ExecResult, error)) error {
	if len(runtime) == 0 {
		return nil
	}
	// A live SSH connection can stop making command progress without tripping
	// keepalives. Never let an install hold a hibernate claim indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	digest := fmt.Sprintf("%x", sha256.Sum256(runtime))
	uploaded, err := execute(ctx, []string{"/usr/local/bin/vmbox-runtime", "put-file", stagedRuntimePath, "0600"}, provider.ExecOptions{Stdin: bytes.NewReader(runtime)})
	if err != nil {
		return fmt.Errorf("stream matching workspace runtime: %w", err)
	}
	if uploaded.ExitCode != 0 || strings.TrimSpace(uploaded.Stdout) != digest {
		return fmt.Errorf("workspace runtime upload failed integrity check with status %d: %s", uploaded.ExitCode, strings.TrimSpace(uploaded.Stderr))
	}
	const install = `set -eu
staged="$1"
installed="$2"
expected="$3"
test "$(sha256sum "$staged" | cut -d " " -f 1)" = "$expected"
chmod 0700 "$staged"
mv -f -- "$staged" "$installed"
exec "$installed" health`
	installed, err := execute(ctx, []string{"sh", "-c", install, "vmbox-install-runtime", stagedRuntimePath, workspaceRuntimePath, digest}, provider.ExecOptions{})
	if err != nil {
		return fmt.Errorf("install matching workspace runtime: %w", err)
	}
	if installed.ExitCode != 0 || strings.TrimSpace(installed.Stdout) != "ok" {
		return fmt.Errorf("installed workspace runtime failed health check with status %d: %s", installed.ExitCode, strings.TrimSpace(installed.Stderr))
	}
	return nil
}

// ensureInitializationSlotRunning restarts a slot that the fleet stopped while
// it was free. A stopped slot has no deployment to reach, so every runtime probe
// against it fails with a transport error rather than a real answer.
func ensureInitializationSlotRunning(ctx context.Context, prov provider.Provider, serviceID string) error {
	actual, err := prov.Inspect(ctx, serviceID)
	if err != nil {
		return fmt.Errorf("inspect initialization slot: %w", err)
	}
	if actual.State != provider.StateStopped {
		return nil
	}
	if _, err := prov.Start(ctx, serviceID); err != nil {
		return fmt.Errorf("start initialization slot: %w", err)
	}
	return nil
}

// probeInitializedWorkspace runs the readiness checks a newly attached workspace
// must pass. The slot is brought up first: probing a stopped slot reports a
// broken workspace that is in fact merely powered down.
func probeInitializedWorkspace(ctx context.Context, prov provider.Provider, serviceID string, runtime []byte) error {
	if err := ensureInitializationSlotRunning(ctx, prov, serviceID); err != nil {
		return err
	}
	if err := stageWorkspaceRuntime(ctx, prov, serviceID, runtime); err != nil {
		return err
	}
	health, err := prov.Exec(ctx, serviceID, []string{"vmbox-runtime", "health"}, provider.ExecOptions{})
	if err != nil {
		return fmt.Errorf("wait for workspace runtime: %w", err)
	}
	if health.ExitCode != 0 || strings.TrimSpace(health.Stdout) != "ok" {
		return fmt.Errorf("workspace runtime health failed with status %d: %s", health.ExitCode, strings.TrimSpace(health.Stderr))
	}
	prepared, err := prov.Exec(ctx, serviceID, []string{"vmbox-runtime", "prepare-hibernate"}, provider.ExecOptions{})
	if err != nil {
		return fmt.Errorf("flush new workspace volume: %w", err)
	}
	if prepared.ExitCode != 0 {
		return fmt.Errorf("new workspace volume remained busy: %s", strings.TrimSpace(prepared.Stderr))
	}
	return nil
}

func (s *Server) finishLogicalBoxCreation(ctx context.Context, creation logicalBoxCreation) error {
	started := time.Now()
	fail := func(err error) error {
		_ = s.Store.FailLogicalBoxCreation(ctx, creation, err.Error())
		s.Logger.Error("logical box creation phase failed", "box", creation.Request.Name, "phase", creation.Assignment.Box.RestorationState, "elapsed", time.Since(started), "error", err)
		return err
	}
	prov, err := s.provider(ctx, creation.AccountID, creation.Request.Provider, creation.Request.ProviderCredential)
	if err != nil {
		return fail(err)
	}
	detachable, ok := prov.(provider.DetachableStorageProvider)
	if !ok {
		return fail(fmt.Errorf("provider %s cannot safely detach a newly initialized workspace", prov.Name()))
	}
	inspector, ok := prov.(provider.AttachedStorageProvider)
	if !ok {
		return fail(fmt.Errorf("provider %s cannot verify exact workspace attachment", prov.Name()))
	}
	serviceID := creation.Assignment.Slot.ServiceID
	if serviceID == "" {
		return fail(fmt.Errorf("reserved compute slot has no service ID"))
	}
	attached, err := inspector.AttachedStorage(ctx, serviceID)
	if err != nil {
		return fail(fmt.Errorf("inspect reserved slot before volume creation: %w", err))
	}
	storage := provider.Storage{ID: creation.Assignment.Box.VolumeID, Name: creation.Assignment.Box.VolumeName, MountPath: "/data", SizeGiB: creation.Request.DiskGiB}
	if pendingVolume(storage.ID) {
		if attached == nil {
			if err := s.Store.UpdateLogicalBoxCreationPhase(ctx, creation, "creation-volume-requested"); err != nil {
				return fail(err)
			}
			creation.Assignment.Box.RestorationState = "creation-volume-requested"
			if owned, ok := prov.(provider.WorkspaceStorageProvider); ok {
				storage, err = owned.CreateWorkspaceStorage(ctx, serviceID, provider.Owner{AccountID: creation.AccountID, BoxID: creation.Assignment.Box.Name}, provider.Resources{DiskGiB: creation.Request.DiskGiB})
			} else {
				storage, err = prov.CreateStorage(ctx, serviceID, provider.Resources{DiskGiB: creation.Request.DiskGiB})
			}
			if err != nil {
				return fail(fmt.Errorf("create workspace volume: %w", err))
			}
		} else {
			if creation.Assignment.Box.RestorationState != "creation-volume-requested" {
				return fail(fmt.Errorf("reserved slot unexpectedly had volume %s before the fenced creation request", attached.ID))
			}
			storage = *attached
			storage.SizeGiB = creation.Request.DiskGiB
		}
		if err := s.Store.PersistLogicalBoxVolume(ctx, creation, storage, "creation-volume-attached"); err != nil {
			return fail(fmt.Errorf("persist created volume identity: %w", err))
		}
		creation.Assignment.Box.VolumeID, creation.Assignment.Box.VolumeName = storage.ID, storage.Name
		creation.Assignment.Box.RestorationState = "creation-volume-attached"
		attached = &storage
	} else {
		if attached == nil {
			if creation.Assignment.Box.RestorationState != "creation-detaching" && creation.Assignment.Box.RestorationState != "creation-sanitizing" {
				return fail(fmt.Errorf("reserved slot is missing workspace volume %s", storage.ID))
			}
		} else if attached.ID != storage.ID {
			return fail(fmt.Errorf("reserved slot contains unrelated volume %s instead of %s", attached.ID, storage.ID))
		}
	}
	if attached != nil {
		if s.DirectWorkersEnabled && creation.Assignment.Box.Provider == "railway" {
			if err := s.Store.UpdateLogicalBoxCreationPhase(ctx, creation, "creation-enrolling-worker"); err != nil {
				return fail(err)
			}
			creation.Assignment.Box.RestorationState = "creation-enrolling-worker"
			if err := ensureInitializationSlotRunning(ctx, prov, serviceID); err != nil {
				return fail(err)
			}
			assignment, err := s.Store.assignment(ctx, creation.AccountID, creation.Assignment.Box.ID)
			if err != nil {
				return fail(fmt.Errorf("reload worker assignment: %w", err))
			}
			if err := s.ensureReplacementWorkerTransport(ctx, creation.AccountID, assignment, prov); err != nil {
				return fail(fmt.Errorf("reconcile worker agent: %w", err))
			}
			bootstrapConnection, err := prov.Connection(ctx, serviceID)
			if err != nil {
				return fail(fmt.Errorf("resolve worker bootstrap: %w", err))
			}
			if err := s.ensureAutomaticWorkerTransport(ctx, creation.AccountID, assignment, prov, bootstrapConnection); err != nil {
				return fail(fmt.Errorf("enroll worker agent: %w", err))
			}
		}
		if err := s.Store.UpdateLogicalBoxCreationPhase(ctx, creation, "creation-initializing"); err != nil {
			return fail(err)
		}
		creation.Assignment.Box.RestorationState = "creation-initializing"
		if err := probeInitializedWorkspace(ctx, prov, serviceID, s.WorkerRuntime); err != nil {
			return fail(err)
		}
		if err := s.provisionCreationProfiles(ctx, prov, creation); err != nil {
			return fail(err)
		}
		// Managed instructions land in the new volume like selected profiles,
		// before the retained workspace detaches for good.
		if err := s.syncBoxInstructions(ctx, prov, creation.AccountID, creation.Assignment.Box.ID, serviceID); err != nil {
			return fail(fmt.Errorf("provision managed instructions: %w", err))
		}
		if len(creation.Request.Tools) > 0 {
			if err := s.Store.UpdateLogicalBoxCreationPhase(ctx, creation, "creation-installing-tools"); err != nil {
				return fail(err)
			}
			toolCtx, cancel := context.WithTimeout(ctx, 9*time.Minute)
			installed, err := prov.Exec(toolCtx, serviceID, append([]string{"vmbox-runtime", "install-tools"}, creation.Request.Tools...), provider.ExecOptions{})
			cancel()
			if err != nil {
				return fail(fmt.Errorf("install selected tools: %w", err))
			}
			if installed.ExitCode != 0 {
				return fail(fmt.Errorf("tool installation failed: %s", strings.TrimSpace(installed.Stderr)))
			}
		}
		if strings.TrimSpace(creation.Request.SetupScript) != "" {
			if err := s.Store.UpdateLogicalBoxCreationPhase(ctx, creation, "creation-installing-tools"); err != nil {
				return fail(err)
			}
			toolCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
			installed, err := prov.Exec(toolCtx, serviceID, []string{"vmbox-runtime", "configure-tools"}, provider.ExecOptions{Stdin: strings.NewReader(creation.Request.SetupScript)})
			cancel()
			if err != nil {
				return fail(fmt.Errorf("install custom tools: %w", err))
			}
			if installed.ExitCode != 0 {
				return fail(fmt.Errorf("custom tool installation failed: %s", strings.TrimSpace(installed.Stderr)))
			}
		}
		if err := s.Store.UpdateLogicalBoxCreationPhase(ctx, creation, "creation-detaching"); err != nil {
			return fail(err)
		}
		creation.Assignment.Box.RestorationState = "creation-detaching"
		if err := detachable.DetachStorage(ctx, serviceID, storage); err != nil {
			return fail(fmt.Errorf("detach retained new workspace: %w", err))
		}
		attached, err = inspector.AttachedStorage(ctx, serviceID)
		if err != nil {
			return fail(fmt.Errorf("verify new workspace detachment: %w", err))
		}
		if attached != nil {
			return fail(fmt.Errorf("volume %s remains attached after detachment", attached.ID))
		}
	}
	if err := s.Store.UpdateLogicalBoxCreationPhase(ctx, creation, "creation-sanitizing"); err != nil {
		return fail(err)
	}
	creation.Assignment.Box.RestorationState = "creation-sanitizing"
	if err := detachable.SanitizeSlot(ctx, serviceID); err != nil {
		return fail(fmt.Errorf("sanitize initialization slot: %w", err))
	}
	if err := s.Store.CompleteLogicalBoxCreation(ctx, creation); err != nil {
		return fail(err)
	}
	s.Logger.Info("logical box volume created and detached", "box", creation.Request.Name, "volume", storage.ID, "elapsed", time.Since(started))
	if creation.Request.ShouldAllocateWhenReady() {
		key := creation.Request.AllocationRequestKey
		if key == "" {
			key = "create-and-allocate:" + creation.Assignment.Box.ID
		}
		principal := Principal{AccountID: creation.AccountID, UserID: creation.UserID, Role: "user", Subject: "logical-box-creator"}
		allocation, err := s.Store.ReserveAllocation(ctx, principal, creation.Assignment.Box.ID, key, "user:"+creation.UserID, 2*time.Minute)
		if err != nil {
			return fmt.Errorf("logical box was created, but allocation failed: %w", err)
		}
		if allocation.State == "reserved" || allocation.State == "attaching" {
			if err := s.activateAllocation(ctx, creation.AccountID, allocation, false); err != nil {
				return fmt.Errorf("logical box was created, but activation failed: %w", err)
			}
		}
	}
	return nil
}

func (s *Server) ReconcileLogicalBoxCreationsNow(ctx context.Context) error {
	creations, err := s.Store.RecoverableLogicalBoxCreations(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for index, err := range finishLogicalBoxCreationRecoveries(ctx, creations, s.finishLogicalBoxCreation) {
		if err != nil {
			failures = append(failures, fmt.Errorf("logical box %s: %w", creations[index].Request.Name, err))
		}
	}
	pending, err := s.Store.PendingAutoStarts(ctx)
	if err != nil {
		failures = append(failures, err)
	} else {
		for _, item := range pending {
			key := item.allocationKey
			if key == "" {
				key = "create-and-allocate:" + item.boxID
			}
			principal := Principal{AccountID: item.accountID, UserID: item.userID, Role: "user", Subject: "logical-box-creator"}
			if _, err := s.Store.ReserveAllocation(ctx, principal, item.boxID, key, "user:"+item.userID, 2*time.Minute); err != nil {
				failures = append(failures, fmt.Errorf("auto-start box %s: %w", item.boxID, err))
			}
		}
	}
	return errorsJoin(failures)
}

// Recovery shares a bounded controller deadline, but independent provider
// operations must start within that window. Otherwise one slow volume blocks
// every newer creation and repeatedly consumes the whole reconciliation pass.
func finishLogicalBoxCreationRecoveries(ctx context.Context, creations []logicalBoxCreation, finish func(context.Context, logicalBoxCreation) error) []error {
	errorsByIndex := make([]error, len(creations))
	semaphore := make(chan struct{}, 4)
	var workers sync.WaitGroup
	for index, creation := range creations {
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
				errorsByIndex[index] = finish(ctx, creation)
			case <-ctx.Done():
				errorsByIndex[index] = ctx.Err()
			}
		}()
	}
	workers.Wait()
	return errorsByIndex
}

func errorsJoin(values []error) error {
	if len(values) == 0 {
		return nil
	}
	var text []string
	for _, value := range values {
		text = append(text, value.Error())
	}
	return fmt.Errorf("%s", strings.Join(text, "; "))
}
