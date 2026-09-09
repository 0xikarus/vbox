package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func pendingVolume(id string) bool { return strings.HasPrefix(id, "pending:") }

const stagedRuntimePath = "/data/home/bin/.vmbox-runtime-staged"
const workspaceRuntimePath = "/data/home/bin/vmbox-runtime"

// stageWorkspaceRuntime keeps a retained volume on the same runtime revision
// as its controller. The audited base image remains immutable; the current,
// credential-free binary is streamed over SSH with mode 0600, verified, then
// atomically installed by the unprivileged workload owner with mode 0700.
func stageWorkspaceRuntime(ctx context.Context, prov provider.Provider, serviceID string, runtime []byte) error {
	if len(runtime) == 0 {
		return nil
	}
	// A live SSH connection can stop making command progress without tripping
	// keepalives. Never let an install hold a hibernate claim indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	digest := fmt.Sprintf("%x", sha256.Sum256(runtime))
	uploaded, err := prov.Exec(ctx, serviceID, []string{"/usr/local/bin/vmbox-runtime", "put-file", stagedRuntimePath, "0600"}, provider.ExecOptions{Stdin: bytes.NewReader(runtime)})
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
	installed, err := prov.Exec(ctx, serviceID, []string{"sh", "-c", install, "vmbox-install-runtime", stagedRuntimePath, workspaceRuntimePath, digest}, provider.ExecOptions{})
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
			storage, err = prov.CreateStorage(ctx, serviceID, provider.Resources{DiskGiB: creation.Request.DiskGiB})
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
	} else if attached != nil && attached.ID != storage.ID {
		return fail(fmt.Errorf("reserved slot contains unrelated volume %s instead of %s", attached.ID, storage.ID))
	}
	if attached != nil {
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
	if creation.Request.AllocateWhenReady {
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
	for _, creation := range creations {
		if err := s.finishLogicalBoxCreation(ctx, creation); err != nil {
			failures = append(failures, fmt.Errorf("logical box %s: %w", creation.Request.Name, err))
		}
	}
	return errorsJoin(failures)
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
