package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

type fleetAssignment struct {
	Box          v1.LogicalBox
	Slot         v1.ComputeSlot
	FencingToken string
	Released     bool
}

func (s *Store) LogicalBox(ctx context.Context, p Principal, id string) (v1.LogicalBox, error) {
	box, err := scanLogicalBox(s.DB.QueryRowContext(ctx, logicalBoxSelect+" WHERE account_id=$1 AND (id::text=$2 OR name=$2)", p.AccountID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return box, fmt.Errorf("logical box not found")
	}
	if err != nil {
		return box, err
	}
	if box.OwnerUserID != p.UserID && p.Role != "owner" {
		return box, fmt.Errorf("logical box belongs to another user")
	}
	return box, nil
}

func (s *Store) ListLogicalBoxes(ctx context.Context, p Principal, providerName, credential string) ([]v1.LogicalBox, error) {
	query := logicalBoxSelect + " WHERE account_id=$1"
	args := []any{p.AccountID}
	if providerName != "" {
		query += " AND provider=$2 AND provider_credential=$3"
		args = append(args, providerName, credential)
	}
	if p.Role != "owner" {
		query += fmt.Sprintf(" AND owner_user_id=$%d", len(args)+1)
		args = append(args, p.UserID)
	}
	query += " ORDER BY name"
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	boxes := []v1.LogicalBox{}
	for rows.Next() {
		box, err := scanLogicalBox(rows)
		if err != nil {
			return nil, err
		}
		boxes = append(boxes, box)
	}
	return boxes, rows.Err()
}

func (s *Store) assignment(ctx context.Context, accountID, logicalBoxID string) (fleetAssignment, error) {
	var assignment fleetAssignment
	box, err := scanLogicalBox(s.DB.QueryRowContext(ctx, logicalBoxSelect+" WHERE account_id=$1 AND id=$2", accountID, logicalBoxID))
	if errors.Is(err, sql.ErrNoRows) {
		return assignment, fmt.Errorf("logical box not found")
	}
	if err != nil {
		return assignment, err
	}
	assignment.Box = box
	if err := s.DB.QueryRowContext(ctx, "SELECT COALESCE(fencing_token,'') FROM logical_boxes WHERE account_id=$1 AND id=$2", accountID, box.ID).Scan(&assignment.FencingToken); err != nil {
		return assignment, err
	}
	if box.SlotID == "" {
		return assignment, nil
	}
	slot, err := scanComputeSlot(s.DB.QueryRowContext(ctx, computeSlotSelect+" WHERE s.account_id=$1 AND s.id=$2", accountID, box.SlotID))
	if err != nil {
		return assignment, err
	}
	assignment.Slot = slot
	return assignment, nil
}

func (s *Store) MarkAssignmentAttaching(ctx context.Context, allocation v1.Allocation) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE logical_boxes SET state='attaching',restoration_state='pending',failure_reason=NULL,updated_at=now() WHERE account_id=(SELECT account_id FROM allocation_requests WHERE id=$1) AND id=$2 AND slot_id=$3 AND assignment_generation=$4 AND fencing_token=$5 AND state IN ('reserved','attaching')", allocation.RequestID, allocation.LogicalBoxID, allocation.SlotID, allocation.AssignmentGeneration, allocation.FencingToken)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale logical-box assignment fencing token")
	}
	result, err = tx.ExecContext(ctx, "UPDATE compute_slots SET state='reserved',failure_reason=NULL,updated_at=now() WHERE account_id=(SELECT account_id FROM allocation_requests WHERE id=$1) AND id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state IN ('reserved','starting')", allocation.RequestID, allocation.SlotID, allocation.AssignmentGeneration, allocation.FencingToken)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale compute-slot assignment fencing token")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE allocation_requests SET state='attaching',phase='attaching-volume',failure_reason=NULL,updated_at=now() WHERE id=$1 AND logical_box_id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state IN ('reserved','attaching')", allocation.RequestID, allocation.LogicalBoxID, allocation.AssignmentGeneration, allocation.FencingToken); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateAllocationProgress(ctx context.Context, accountID, requestID, phase, failure string, retry bool) error {
	increment := 0
	if retry {
		increment = 1
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE allocation_requests SET phase=$3,failure_reason=NULLIF($4,''),retry_count=retry_count+$5,updated_at=now() WHERE account_id=$1 AND id=$2", accountID, requestID, phase, failure, increment)
	return err
}

func (s *Store) RenewAssignmentLease(ctx context.Context, accountID string, allocation v1.Allocation, duration time.Duration) error {
	if duration <= 0 {
		duration = 2 * time.Minute
	}
	expires := time.Now().UTC().Add(duration)
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE logical_boxes SET lease_expires_at=$5,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state IN ('reserved','attaching')", accountID, allocation.LogicalBoxID, allocation.AssignmentGeneration, allocation.FencingToken, expires)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale logical-box lease")
	}
	result, err = tx.ExecContext(ctx, "UPDATE compute_slots SET lease_expires_at=$5,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state='reserved'", accountID, allocation.SlotID, allocation.AssignmentGeneration, allocation.FencingToken, expires)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale compute-slot lease")
	}
	return tx.Commit()
}

func scanAllocation(scanner interface{ Scan(...any) error }) (v1.Allocation, error) {
	var allocation v1.Allocation
	var lease sql.NullTime
	err := scanner.Scan(
		&allocation.RequestID, &allocation.IdempotencyKey, &allocation.State,
		&allocation.LogicalBoxID, &allocation.LogicalBoxName, &allocation.SlotID,
		&allocation.ServiceID, &allocation.AssignmentGeneration, &allocation.FencingToken,
		&allocation.LeaseOwner, &lease, &allocation.Phase, &allocation.RetryCount,
		&allocation.FailureReason, &allocation.CreatedAt, &allocation.UpdatedAt,
	)
	if lease.Valid {
		allocation.LeaseExpiresAt = &lease.Time
	}
	return allocation, err
}

const allocationSelect = "SELECT r.id::text,r.idempotency_key,r.state,r.logical_box_id::text,b.name,COALESCE(r.slot_id::text,''),COALESCE(s.service_id,''),COALESCE(r.assignment_generation,0),COALESCE(r.fencing_token,''),COALESCE(b.lease_owner,''),b.lease_expires_at,COALESCE(r.phase,''),r.retry_count,COALESCE(r.failure_reason,''),r.created_at,r.updated_at FROM allocation_requests r JOIN logical_boxes b ON b.id=r.logical_box_id LEFT JOIN compute_slots s ON s.id=r.slot_id"

func (s *Store) Allocation(ctx context.Context, accountID, id string) (v1.Allocation, error) {
	allocation, err := scanAllocation(s.DB.QueryRowContext(ctx, allocationSelect+" WHERE r.account_id=$1 AND r.id=$2", accountID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return allocation, fmt.Errorf("allocation not found")
	}
	if err == nil && allocation.State == "queued" {
		_ = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM allocation_requests WHERE account_id=$1 AND state='queued' AND (created_at,id)<($2,$3::uuid)", accountID, allocation.CreatedAt, allocation.RequestID).Scan(&allocation.QueuePosition)
		allocation.QueuePosition++
	}
	return allocation, err
}

func (s *Store) RecoverableAllocations(ctx context.Context) ([]struct {
	AccountID  string
	Allocation v1.Allocation
}, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT account_id::text,id::text FROM allocation_requests WHERE state IN ('reserved','attaching') ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ accountID, id string }
	var keys []key
	for rows.Next() {
		var value key
		if err := rows.Scan(&value.accountID, &value.id); err != nil {
			return nil, err
		}
		keys = append(keys, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]struct {
		AccountID  string
		Allocation v1.Allocation
	}, 0, len(keys))
	for _, value := range keys {
		allocation, err := s.Allocation(ctx, value.accountID, value.id)
		if err != nil {
			return nil, err
		}
		result = append(result, struct {
			AccountID  string
			Allocation v1.Allocation
		}{AccountID: value.accountID, Allocation: allocation})
	}
	return result, nil
}

func (s *Store) BeginLogicalBoxRelease(ctx context.Context, p Principal, id string, target v1.LogicalBoxState) (fleetAssignment, error) {
	var assignment fleetAssignment
	if target != v1.LogicalBoxHibernating && target != v1.LogicalBoxDeleting {
		return assignment, fmt.Errorf("invalid logical-box release target %s", target)
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return assignment, err
	}
	defer tx.Rollback()
	box, err := scanLogicalBox(tx.QueryRowContext(ctx, logicalBoxSelect+" WHERE account_id=$1 AND (id::text=$2 OR name=$2) FOR UPDATE", p.AccountID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return assignment, fmt.Errorf("logical box not found")
	}
	if err != nil {
		return assignment, err
	}
	if box.OwnerUserID != p.UserID && p.Role != "owner" {
		return assignment, fmt.Errorf("logical box belongs to another user")
	}
	assignment.Box = box
	if target == v1.LogicalBoxHibernating && (box.State == v1.LogicalBoxHibernated || box.State == v1.LogicalBoxDetached) {
		assignment.Released = true
		return assignment, tx.Commit()
	}
	if target == v1.LogicalBoxDeleting && (box.State == v1.LogicalBoxHibernated || box.State == v1.LogicalBoxDetached) {
		if _, err := tx.ExecContext(ctx, "UPDATE logical_boxes SET state='deleting',failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2", p.AccountID, box.ID); err != nil {
			return assignment, err
		}
		assignment.Box.State = v1.LogicalBoxDeleting
		return assignment, tx.Commit()
	}
	if box.SlotID == "" || (box.State != v1.LogicalBoxRunning && box.State != v1.LogicalBoxDraining && box.State != v1.LogicalBoxHibernating && box.State != v1.LogicalBoxDeleting) {
		return assignment, fmt.Errorf("logical box %q cannot transition from %s", box.Name, box.State)
	}
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(fencing_token,'') FROM logical_boxes WHERE account_id=$1 AND id=$2", p.AccountID, box.ID).Scan(&assignment.FencingToken); err != nil {
		return assignment, err
	}
	slot, err := scanComputeSlot(tx.QueryRowContext(ctx, computeSlotSelect+" WHERE s.account_id=$1 AND s.id=$2 FOR UPDATE OF s", p.AccountID, box.SlotID))
	if err != nil {
		return assignment, err
	}
	if assignment.FencingToken == "" || slot.AssignmentGeneration != box.AssignmentGeneration {
		return assignment, fmt.Errorf("logical box assignment is missing a valid fence")
	}
	assignment.Slot = slot
	expires := time.Now().UTC().Add(5 * time.Minute)
	result, err := tx.ExecContext(ctx, "UPDATE logical_boxes SET state=$5,lease_expires_at=$6,failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4", p.AccountID, box.ID, box.AssignmentGeneration, assignment.FencingToken, target, expires)
	if err != nil {
		return assignment, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return assignment, fmt.Errorf("stale logical-box release fence")
	}
	result, err = tx.ExecContext(ctx, "UPDATE compute_slots SET state='draining',lease_expires_at=$5,failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state IN ('occupied','draining')", p.AccountID, slot.ID, box.AssignmentGeneration, assignment.FencingToken, expires)
	if err != nil {
		return assignment, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return assignment, fmt.Errorf("stale compute-slot release fence")
	}
	assignment.Box.State = target
	return assignment, tx.Commit()
}

func (s *Store) RecordReleaseFailure(ctx context.Context, accountID string, assignment fleetAssignment, reason string) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE logical_boxes SET failure_reason=$5,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4", accountID, assignment.Box.ID, assignment.Box.AssignmentGeneration, assignment.FencingToken, reason); err != nil {
		return err
	}
	if assignment.Slot.ID != "" {
		if _, err := tx.ExecContext(ctx, "UPDATE compute_slots SET state='draining',failure_reason=$5,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4", accountID, assignment.Slot.ID, assignment.Box.AssignmentGeneration, assignment.FencingToken, reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteLogicalBoxRecord(ctx context.Context, p Principal, assignment fleetAssignment) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if assignment.Slot.ID != "" {
		result, err := tx.ExecContext(ctx, "UPDATE compute_slots SET state='free',lease_owner=NULL,lease_expires_at=NULL,fencing_token=NULL,deployment_instance_id=NULL,failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state='draining'", p.AccountID, assignment.Slot.ID, assignment.Box.AssignmentGeneration, assignment.FencingToken)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return fmt.Errorf("stale compute-slot delete fence")
		}
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state='deleting' AND ($3='' OR (assignment_generation=$4 AND fencing_token=$3))", p.AccountID, assignment.Box.ID, assignment.FencingToken, assignment.Box.AssignmentGeneration)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale logical-box delete fence")
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'logical_box.volume.delete','logical_box',$3,jsonb_build_object('volume_id',$4,'volume_name',$5))", p.AccountID, p.UserID, assignment.Box.ID, assignment.Box.VolumeID, assignment.Box.VolumeName); err != nil {
		return err
	}
	return tx.Commit()
}
