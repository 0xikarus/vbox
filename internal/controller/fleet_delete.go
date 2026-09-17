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

func (s *Server) queueLogicalBoxDelete(ctx context.Context, p Principal, id, confirmation string) (v1.LogicalBox, error) {
	box, err := s.Store.LogicalBox(ctx, p, id)
	if err != nil {
		return box, err
	}
	if confirmation == "" || confirmation != box.Name {
		return box, fmt.Errorf("deletion confirmation must exactly match logical box name %q", box.Name)
	}
	a, err := s.Store.BeginLogicalBoxRelease(ctx, p, box.ID, v1.LogicalBoxDeleting)
	if err != nil {
		return box, err
	}
	// Do not disturb a worker's claim when the owner repeats the request.
	_, err = s.Store.DB.ExecContext(ctx, `UPDATE logical_boxes SET lease_owner=NULL,lease_expires_at=NULL,restoration_state='delete-queued',updated_at=now() WHERE account_id=$1 AND id=$2 AND state='deleting' AND COALESCE(lease_owner,'') NOT LIKE 'delete_%' AND restoration_state NOT LIKE 'delete-%'`, p.AccountID, a.Box.ID)
	if err != nil {
		return box, err
	}
	return s.Store.LogicalBox(ctx, p, a.Box.ID)
}

func (s *Server) startLogicalBoxDelete(p Principal, id string) {
	run := s.resumeLogicalBoxDelete
	if s.StartDelete != nil {
		run = s.StartDelete
	}
	go func() {
		// Independent of the HTTP request, but bounded. A later reconciler
		// resumes from provider observations rather than replaying a blind flush.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := run(ctx, p, id); err != nil {
			s.Logger.Warn("logical box deletion attempt stopped", "box", id, "error", err)
		}
	}()
}

func (s *Server) ReconcileLogicalBoxDeletesNow(ctx context.Context) error {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT account_id::text,id::text,owner_user_id::text FROM logical_boxes WHERE state='deleting' AND (lease_expires_at IS NULL OR lease_expires_at < now()) ORDER BY updated_at,id`)
	if err != nil {
		return err
	}
	var values []pendingLogicalBoxHibernate
	for rows.Next() {
		var value pendingLogicalBoxHibernate
		if err := rows.Scan(&value.AccountID, &value.BoxID, &value.OwnerUserID); err != nil {
			rows.Close()
			return err
		}
		values = append(values, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, value := range values {
		s.startLogicalBoxDelete(Principal{AccountID: value.AccountID, UserID: value.OwnerUserID, Role: "user", Subject: "controller:delete-reconciler"}, value.BoxID)
	}
	return nil
}

func (s *Server) resumeLogicalBoxDelete(ctx context.Context, p Principal, id string) error {
	box, err := s.Store.LogicalBox(ctx, p, id)
	if err != nil {
		return err
	}
	if box.State != v1.LogicalBoxDeleting {
		return fmt.Errorf("no confirmed deletion is pending")
	}
	a, err := s.Store.BeginLogicalBoxRelease(ctx, p, id, v1.LogicalBoxDeleting)
	if err != nil {
		return err
	}
	claim := "delete_" + strings.ReplaceAll(uuid(), "-", "")
	result, err := s.Store.DB.ExecContext(ctx, `UPDATE logical_boxes SET lease_owner=$3,lease_expires_at=now()+interval '45 seconds',updated_at=now() WHERE account_id=$1 AND id=$2 AND state='deleting' AND assignment_generation=$4 AND COALESCE(fencing_token,'')=$5 AND (lease_expires_at IS NULL OR lease_expires_at < now())`, p.AccountID, a.Box.ID, claim, a.Box.AssignmentGeneration, a.FencingToken)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return nil
	}
	operationCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-operationCtx.Done():
				done <- nil
				return
			case <-ticker.C:
				result, err := s.Store.DB.ExecContext(operationCtx, `UPDATE logical_boxes SET lease_expires_at=now()+interval '45 seconds' WHERE account_id=$1 AND id=$2 AND state='deleting' AND lease_owner=$3`, p.AccountID, a.Box.ID, claim)
				if err == nil {
					if n, _ := result.RowsAffected(); n != 1 {
						err = fmt.Errorf("deletion claim lost")
					}
				}
				if err != nil {
					cancel()
					done <- err
					return
				}
			}
		}
	}()
	err = s.completeLogicalBoxDelete(operationCtx, p, a, claim)
	cancel()
	heartbeatErr := <-done
	if err == nil {
		return nil
	}
	err = errors.Join(err, heartbeatErr)
	// The failed request/operation context cannot reliably persist diagnostics.
	settleCtx, settleCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer settleCancel()
	_, recordErr := s.Store.DB.ExecContext(settleCtx, `UPDATE logical_boxes SET failure_reason=$4,lease_owner=NULL,lease_expires_at=now()+interval '30 seconds',updated_at=now() WHERE account_id=$1 AND id=$2 AND state='deleting' AND lease_owner=$3`, p.AccountID, a.Box.ID, claim, err.Error())
	return errors.Join(err, recordErr)
}

func (s *Server) completeLogicalBoxDelete(ctx context.Context, p Principal, a fleetAssignment, claim string) error {
	phase := func(value string) error {
		result, err := s.Store.DB.ExecContext(ctx, `UPDATE logical_boxes b SET restoration_state=$4,failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND state='deleting' AND lease_owner=$3 AND assignment_generation=$5 AND COALESCE(fencing_token,'')=$6 AND (slot_id IS NULL OR EXISTS(SELECT 1 FROM compute_slots s WHERE s.id=b.slot_id AND s.account_id=b.account_id AND s.state='draining' AND s.assignment_generation=b.assignment_generation AND s.fencing_token=b.fencing_token))`, p.AccountID, a.Box.ID, claim, value, a.Box.AssignmentGeneration, a.FencingToken)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return fmt.Errorf("deletion assignment or claim changed")
		}
		return nil
	}
	if err := phase("delete-inspecting-volume"); err != nil {
		return err
	}
	prov, err := s.provider(ctx, p.AccountID, a.Box.Provider, a.Box.ProviderCredential)
	if err != nil {
		return err
	}
	storage := provider.Storage{ID: a.Box.VolumeID, Name: a.Box.VolumeName, MountPath: "/data"}
	var detachable provider.DetachableStorageProvider
	if a.Slot.ID != "" {
		var ok bool
		detachable, ok = prov.(provider.DetachableStorageProvider)
		if !ok {
			return fmt.Errorf("provider lacks safe volume detachment")
		}
		inspector, ok := prov.(provider.AttachedStorageProvider)
		if !ok {
			return fmt.Errorf("provider lacks independent attached-volume inspection")
		}
		attached, err := inspector.AttachedStorage(ctx, a.Slot.ServiceID)
		if err != nil {
			return fmt.Errorf("inspect attached volume: %w", err)
		}
		if attached != nil {
			if attached.ID != storage.ID {
				return fmt.Errorf("slot has another volume attached; deletion refused")
			}
			if err := phase("delete-saving-workspace"); err != nil {
				return err
			}
			if err := stageWorkspaceRuntime(ctx, prov, a.Slot.ServiceID, s.WorkerRuntime); err != nil {
				return err
			}
			prepared, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "prepare-hibernate"}, provider.ExecOptions{})
			if err != nil {
				return fmt.Errorf("stop workload and flush: %w", err)
			}
			if prepared.ExitCode != 0 {
				return fmt.Errorf("workspace flush failed; deletion stopped")
			}
			if err := phase("delete-detaching-volume"); err != nil {
				return err
			}
			if err := detachable.DetachStorage(ctx, a.Slot.ServiceID, storage); err != nil {
				return err
			}
		}
		if err := phase("delete-verifying-detach"); err != nil {
			return err
		}
		attached, err = inspector.AttachedStorage(ctx, a.Slot.ServiceID)
		if err != nil {
			return err
		}
		if attached != nil {
			return fmt.Errorf("volume still attached; deletion refused")
		}
	}
	if err := phase("delete-volume"); err != nil {
		return err
	}
	if err := prov.DeleteStorage(ctx, storage, provider.Owner{AccountID: p.AccountID, BoxID: a.Box.Name}); err != nil {
		return err
	}
	if a.Slot.ID != "" {
		if err := phase("delete-sanitizing-compute"); err != nil {
			return err
		}
		if err := detachable.SanitizeSlot(ctx, a.Slot.ServiceID); err != nil {
			return err
		}
	}
	if err := phase("delete-finalizing"); err != nil {
		return err
	}
	return s.Store.DeleteLogicalBoxRecord(ctx, p, a, claim)
}
