package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func pendingVolume(id string) bool { return strings.HasPrefix(id, "pending:") }

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
		health, err := prov.Exec(ctx, serviceID, []string{"vmbox-runtime", "health"}, provider.ExecOptions{})
		if err != nil {
			return fail(fmt.Errorf("wait for workspace runtime: %w", err))
		}
		if health.ExitCode != 0 || strings.TrimSpace(health.Stdout) != "ok" {
			return fail(fmt.Errorf("workspace runtime health failed with status %d: %s", health.ExitCode, strings.TrimSpace(health.Stderr)))
		}
		prepared, err := prov.Exec(ctx, serviceID, []string{"vmbox-runtime", "prepare-hibernate"}, provider.ExecOptions{})
		if err != nil {
			return fail(fmt.Errorf("flush new workspace volume: %w", err))
		}
		if prepared.ExitCode != 0 {
			return fail(fmt.Errorf("new workspace volume remained busy: %s", strings.TrimSpace(prepared.Stderr)))
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
