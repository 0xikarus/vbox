package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

// CancelUnmaterializedBoxCreation removes a failed creation with only a
// placeholder volume. The caller verifies that the provider service is gone
// unless the creation failed before requesting any volume.
func (s *Store) CancelUnmaterializedBoxCreation(ctx context.Context, p Principal, box v1.LogicalBox) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE compute_slots s SET state=CASE WHEN $5 THEN 'unhealthy' ELSE 'free' END,health='unhealthy',lease_owner=NULL,lease_expires_at=NULL,fencing_token=NULL,deployment_instance_id=NULL,failure_reason='failed creation before volume allocation',updated_at=now()
		FROM logical_boxes b WHERE b.account_id=$1 AND b.id=$2 AND b.name=$3 AND b.owner_user_id=$4 AND b.state IN ('attaching','failed') AND b.failure_reason IS NOT NULL AND b.failure_reason<>'' AND b.volume_id LIKE 'pending:%'
		AND s.id=b.slot_id AND s.account_id=b.account_id AND s.assignment_generation=b.assignment_generation AND s.fencing_token=b.fencing_token AND s.state='draining'`, p.AccountID, box.ID, box.Name, box.OwnerUserID, box.State == v1.LogicalBoxFailed)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("failed creation changed while cancelling; retry")
	}
	result, err = tx.ExecContext(ctx, `DELETE FROM logical_boxes WHERE account_id=$1 AND id=$2 AND name=$3 AND owner_user_id=$4 AND state IN ('attaching','failed') AND failure_reason IS NOT NULL AND failure_reason<>'' AND volume_id LIKE 'pending:%'`, p.AccountID, box.ID, box.Name, box.OwnerUserID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("failed creation changed while cancelling; retry")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'logical_box.creation.cancel','logical_box',$3,'{"volume":"not_created"}'::jsonb)`, p.AccountID, p.UserID, box.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) cancelUnmaterializedBoxCreation(ctx context.Context, p Principal, box v1.LogicalBox) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, active := s.activeCreations[p.AccountID+":"+box.ID]; active {
		return fmt.Errorf("logical box creation is still active; retry deletion shortly")
	}
	return s.Store.CancelUnmaterializedBoxCreation(ctx, p, box)
}

func (s *Server) missingProviderCompute(ctx context.Context, accountID string, box v1.LogicalBox) (bool, error) {
	assignment, err := s.Store.assignment(ctx, accountID, box.ID)
	if err != nil {
		return false, err
	}
	if assignment.Slot.ServiceID == "" {
		return false, fmt.Errorf("compute slot has no provider service identity")
	}
	prov, err := s.provider(ctx, accountID, box.Provider, box.ProviderCredential)
	if err != nil {
		return false, err
	}
	_, err = prov.Inspect(ctx, assignment.Slot.ServiceID)
	if errors.Is(err, provider.ErrNotFound) {
		return true, nil
	}
	return false, err
}

func (s *Server) markMissingAttach(ctx context.Context, p Principal, box v1.LogicalBox) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, active := s.activeCreations[p.AccountID+":"+box.ID]; active {
		return fmt.Errorf("logical box creation is still active; retry deletion shortly")
	}
	missing, err := s.missingProviderCompute(ctx, p.AccountID, box)
	if err != nil {
		return err
	}
	if !missing {
		return fmt.Errorf("logical box attachment is still active")
	}
	return s.Store.FailMissingAttach(ctx, p, box)
}

func (s *Server) queueLogicalBoxDelete(ctx context.Context, p Principal, id, confirmation string) (v1.LogicalBox, error) {
	box, err := s.Store.LogicalBox(ctx, p, id)
	if err != nil {
		return box, err
	}
	if confirmation == "" || confirmation != box.Name {
		return box, fmt.Errorf("deletion confirmation must exactly match logical box name %q", box.Name)
	}
	if box.State == v1.LogicalBoxAttaching {
		// A failed creation is retried by the reconciler. Serialize its
		// cancellation with the in-process creation claim, then change its
		// durable state before another attempt may begin.
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, active := s.activeCreations[p.AccountID+":"+box.ID]; active {
			return box, fmt.Errorf("logical box creation is still active; retry deletion shortly")
		}
		if box.FailureReason == "" {
			missing, err := s.missingProviderCompute(ctx, p.AccountID, box)
			if err != nil {
				return box, err
			}
			if !missing {
				return box, fmt.Errorf("logical box attachment is still active")
			}
			if err := s.Store.FailMissingAttach(ctx, p, box); err != nil {
				return box, err
			}
		}
	}
	a, err := s.Store.BeginLogicalBoxRelease(ctx, p, box.ID, v1.LogicalBoxDeleting)
	if err != nil {
		return box, err
	}
	// Do not disturb a worker's claim when the owner repeats the request.
	_, err = s.Store.DB.ExecContext(ctx, `UPDATE logical_boxes SET lease_owner=NULL,lease_expires_at=NULL,restoration_state='delete-queued',updated_at=now() WHERE account_id=$1 AND id=$2 AND state='deleting' AND COALESCE(lease_owner,'') NOT LIKE 'delete_%' AND COALESCE(restoration_state,'') NOT LIKE 'delete-%'`, p.AccountID, a.Box.ID)
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
		if errors.Is(err, provider.ErrNotFound) {
			a.MissingCompute = true
			err = nil
		} else if err != nil {
			return fmt.Errorf("inspect attached volume: %w", err)
		}
		if attached != nil {
			if attached.ID != storage.ID {
				return fmt.Errorf("slot has another volume attached; deletion refused")
			}
			// A shared worker's detach stops only this workspace's UID-owned
			// processes before releasing its slot. Deletion discards the volume,
			// so uploading a workspace runtime and saving a hibernation snapshot
			// is unnecessary and can strand deletion when the worker is busy.
			// Dedicated providers still need the in-workspace stop and flush
			// before their external volume detach.
			if prov.Name() != "shared-worker" {
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
		if !a.MissingCompute {
			attached, err = inspector.AttachedStorage(ctx, a.Slot.ServiceID)
		}
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
	if err := prov.DeleteStorage(ctx, storage, provider.Owner{AccountID: p.AccountID, BoxID: a.Box.Name}); err != nil && !errors.Is(err, provider.ErrNotFound) {
		return err
	}
	if a.Slot.ID != "" {
		if err := phase("delete-sanitizing-compute"); err != nil {
			return err
		}
		if !a.MissingCompute {
			if err := detachable.SanitizeSlot(ctx, a.Slot.ServiceID); err != nil {
				return err
			}
		}
	}
	if err := phase("delete-finalizing"); err != nil {
		return err
	}
	return s.Store.DeleteLogicalBoxRecord(ctx, p, a, claim)
}
