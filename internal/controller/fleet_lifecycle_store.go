package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

type fleetAssignment struct {
	Box            v1.LogicalBox
	Slot           v1.ComputeSlot
	FencingToken   string
	Released       bool
	MissingCompute bool
}

func (s *Store) LogicalBox(ctx context.Context, p Principal, id string) (v1.LogicalBox, error) {
	box, err := scanLogicalBox(s.DB.QueryRowContext(ctx, logicalBoxSelect+" WHERE account_id=$1 AND (id::text=$2 OR name=$2)", p.AccountID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return box, fmt.Errorf("logical box not found")
	}
	if err != nil {
		return box, err
	}
	if box.OwnerUserID != p.UserID && p.Role != "owner" && !(p.Role == "desktop-agent" && p.Subject == "desktop-box:"+box.ID) {
		return box, fmt.Errorf("logical box belongs to another user")
	}
	return box, nil
}

func (s *Store) UpdateLogicalBox(ctx context.Context, p Principal, id string, request v1.UpdateLogicalBoxRequest) (v1.LogicalBox, error) {
	request.DefaultAgent = strings.ToLower(strings.TrimSpace(request.DefaultAgent))
	if !validAgent(request.DefaultAgent) {
		return v1.LogicalBox{}, fmt.Errorf("default agent must be codex, claude, opencode, or shell")
	}
	if request.Role != nil {
		return v1.LogicalBox{}, fmt.Errorf("the worker/manager role field is obsolete; use native role assignments")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return v1.LogicalBox{}, err
	}
	defer tx.Rollback()
	var rawProfiles []byte
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(metadata->'importedLoginProfiles',metadata->'loginProfiles','[]'::jsonb) FROM logical_boxes WHERE account_id=$1 AND (id::text=$2 OR name=$2) AND (owner_user_id=$3 OR $4='owner') FOR UPDATE`, p.AccountID, id, p.UserID, p.Role).Scan(&rawProfiles); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return v1.LogicalBox{}, fmt.Errorf("logical box not found")
		}
		return v1.LogicalBox{}, err
	}
	var profiles []v1.LoginProfileRef
	if json.Unmarshal(rawProfiles, &profiles) != nil {
		return v1.LogicalBox{}, fmt.Errorf("imported login profile state is invalid")
	}
	requiredAgent := selectedProfileAgent(request.DefaultAgent, profiles)
	if requiredAgent != request.DefaultAgent {
		return v1.LogicalBox{}, fmt.Errorf("selected %s profile requires the %s harness; change credentials instead", profiles[0].Application, requiredAgent)
	}
	result, err := tx.ExecContext(ctx, `UPDATE logical_boxes SET default_agent=$5,updated_at=now() WHERE account_id=$1 AND (id::text=$2 OR name=$2) AND (owner_user_id=$3 OR $4='owner')`, p.AccountID, id, p.UserID, p.Role, request.DefaultAgent)
	if err != nil {
		return v1.LogicalBox{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return v1.LogicalBox{}, fmt.Errorf("logical box not found")
	}
	box, err := scanLogicalBox(tx.QueryRowContext(ctx, logicalBoxSelect+` WHERE account_id=$1 AND (id::text=$2 OR name=$2)`, p.AccountID, id))
	if err != nil {
		return v1.LogicalBox{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'logical_box.settings.update','logical_box',$3,jsonb_build_object('default_agent',$4::text))`, p.AccountID, p.UserID, box.ID, request.DefaultAgent); err != nil {
		return v1.LogicalBox{}, err
	}
	return box, tx.Commit()
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return boxes, nil
}

func (s *Store) assignment(ctx context.Context, accountID, logicalBoxID string) (fleetAssignment, error) {
	var assignment fleetAssignment
	box, err := scanLogicalBox(s.DB.QueryRowContext(ctx, logicalBoxSelect+" WHERE account_id=$1 AND id=$2", accountID, logicalBoxID))
	if errors.Is(err, sql.ErrNoRows) {
		return assignment, errLogicalBoxMissing
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
	if _, err := tx.ExecContext(ctx, "UPDATE allocation_requests SET state='attaching',attach_started_at=COALESCE(attach_started_at,now()),phase='attaching-volume',failure_reason=NULL,updated_at=now() WHERE id=$1 AND logical_box_id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state IN ('reserved','attaching')", allocation.RequestID, allocation.LogicalBoxID, allocation.AssignmentGeneration, allocation.FencingToken); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateAllocationProgress(ctx context.Context, accountID, requestID, phase, failure string, retry bool) error {
	increment := 0
	if retry {
		increment = 1
	}
	// A concurrent recovery attempt may finish after another worker has already
	// made the allocation ready. Progress from that stale worker must not replace
	// the terminal phase or reintroduce a failure reason.
	_, err := s.DB.ExecContext(ctx, "UPDATE allocation_requests SET phase=$3,failure_reason=NULLIF($4,''),retry_count=retry_count+$5,updated_at=now() WHERE account_id=$1 AND id=$2 AND state IN ('queued','reserved','attaching')", accountID, requestID, phase, failure, increment)
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
	if target == v1.LogicalBoxDeleting && box.State == v1.LogicalBoxHibernating && strings.HasPrefix(box.LeaseOwner, "hibernate_") && box.LeaseExpiresAt != nil && box.LeaseExpiresAt.After(time.Now()) {
		return assignment, fmt.Errorf("workspace flush is active; wait for hibernation before confirming deletion")
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
	// A detached delete may have failed after its durable state transition but
	// before the provider accepted the exact volume deletion. There is no slot
	// or fence to reacquire in that case; resume from the saved volume identity.
	if target == v1.LogicalBoxDeleting && box.State == v1.LogicalBoxDeleting && box.SlotID == "" {
		return assignment, tx.Commit()
	}
	failedCreation := target == v1.LogicalBoxDeleting && (box.State == v1.LogicalBoxFailed || box.State == v1.LogicalBoxAttaching && box.FailureReason != "") && box.VolumeID != "" && !pendingVolume(box.VolumeID)
	if box.SlotID == "" || (!failedCreation && box.State != v1.LogicalBoxRunning && box.State != v1.LogicalBoxDraining && box.State != v1.LogicalBoxHibernating && box.State != v1.LogicalBoxDeleting) {
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
	if failedCreation && slot.State != "draining" {
		return assignment, fmt.Errorf("failed logical box creation has not released its compute claim")
	}
	assignment.Slot = slot
	if check, ok := ctx.Value(idleReleaseCheckKey{}).(idleReleaseCheck); ok {
		if err := check(ctx, tx, assignment); err != nil {
			return assignment, err
		}
	}
	if target == v1.LogicalBoxHibernating && box.State == v1.LogicalBoxHibernating {
		return assignment, tx.Commit()
	}
	if target == v1.LogicalBoxDeleting && box.State == v1.LogicalBoxDeleting {
		return assignment, tx.Commit()
	}
	expires := time.Now().UTC().Add(5 * time.Minute)
	result, err := tx.ExecContext(ctx, "UPDATE logical_boxes SET state=$5,lease_owner=CASE WHEN $5='hibernating' THEN NULL ELSE lease_owner END,lease_expires_at=CASE WHEN $5='hibernating' THEN NULL ELSE $6::timestamptz END,restoration_state=CASE WHEN $5='hibernating' THEN 'hibernate-queued' ELSE restoration_state END,failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4", p.AccountID, box.ID, box.AssignmentGeneration, assignment.FencingToken, target, expires)
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
	if target == v1.LogicalBoxHibernating {
		assignment.Box.LeaseOwner = ""
		assignment.Box.LeaseExpiresAt = nil
		assignment.Box.RestorationState = "hibernate-queued"
	}
	return assignment, tx.Commit()
}

type pendingLogicalBoxHibernate struct {
	AccountID   string
	BoxID       string
	OwnerUserID string
}

func (s *Store) PendingLogicalBoxHibernates(ctx context.Context) ([]pendingLogicalBoxHibernate, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT account_id::text,id::text,owner_user_id::text FROM logical_boxes WHERE state='hibernating' ORDER BY updated_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []pendingLogicalBoxHibernate
	for rows.Next() {
		var value pendingLogicalBoxHibernate
		if err := rows.Scan(&value.AccountID, &value.BoxID, &value.OwnerUserID); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) ClaimLogicalBoxHibernate(ctx context.Context, accountID, boxID string) (string, bool, error) {
	token := "hibernate_" + strings.ReplaceAll(uuid(), "-", "")
	result, err := s.DB.ExecContext(ctx, `UPDATE logical_boxes SET lease_owner=$3,lease_expires_at=now()+interval '45 seconds',restoration_state=CASE WHEN restoration_state LIKE 'auto-%' THEN 'auto-saving-workspace' ELSE 'saving-workspace' END,failure_reason=NULL,updated_at=now()
		WHERE account_id=$1 AND id=$2 AND state='hibernating' AND (lease_owner IS NULL OR lease_expires_at IS NULL OR lease_expires_at < now())`, accountID, boxID, token)
	if err != nil {
		return "", false, err
	}
	changed, _ := result.RowsAffected()
	return token, changed == 1, nil
}

func (s *Store) RenewLogicalBoxHibernate(ctx context.Context, accountID, boxID, token string) (bool, error) {
	result, err := s.DB.ExecContext(ctx, `UPDATE logical_boxes SET lease_expires_at=now()+interval '45 seconds' WHERE account_id=$1 AND id=$2 AND state='hibernating' AND lease_owner=$3`, accountID, boxID, token)
	if err != nil {
		return false, err
	}
	changed, _ := result.RowsAffected()
	return changed == 1, nil
}

func (s *Store) SetLogicalBoxHibernatePhase(ctx context.Context, accountID, boxID, token, phase string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE logical_boxes SET restoration_state=CASE WHEN restoration_state LIKE 'auto-%' THEN 'auto-' || $4 ELSE $4 END,failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND state='hibernating' AND lease_owner=$3`, accountID, boxID, token, phase)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("logical-box hibernate claim was lost")
	}
	return nil
}

func (s *Store) ReleaseLogicalBoxHibernateClaim(ctx context.Context, accountID, boxID, token string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE logical_boxes SET lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND state='hibernating' AND lease_owner=$3`, accountID, boxID, token)
	return err
}

func (s *Store) RecordReleaseFailure(ctx context.Context, accountID string, assignment fleetAssignment, reason string) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE logical_boxes SET failure_reason=$5,updated_at=now() WHERE account_id=$1 AND id=$2 AND ($4='' OR (assignment_generation=$3 AND fencing_token=$4))", accountID, assignment.Box.ID, assignment.Box.AssignmentGeneration, assignment.FencingToken, reason); err != nil {
		return err
	}
	if assignment.Slot.ID != "" {
		if _, err := tx.ExecContext(ctx, "UPDATE compute_slots SET state='draining',failure_reason=$5,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4", accountID, assignment.Slot.ID, assignment.Box.AssignmentGeneration, assignment.FencingToken, reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteLogicalBoxRecord(ctx context.Context, p Principal, assignment fleetAssignment, claims ...string) error {
	claim := ""
	if len(claims) > 0 {
		claim = claims[0]
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Remove only this box's inbox and token. Keep messages in sibling inboxes
	// with a tombstoned sender and a fresh key outside the owner's retry keys.
	if _, err := tx.ExecContext(ctx, `DELETE FROM coworker_events WHERE account_id=$1 AND recipient_box_id=$2`, p.AccountID, assignment.Box.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE coworker_events SET sender_box_id=NULL,message_key=$3 || ':' || sequence::text,data=data || jsonb_build_object('deletedSenderBox',$4::text) WHERE account_id=$1 AND sender_box_id=$2`, p.AccountID, assignment.Box.ID, "deleted:"+uuid(), assignment.Box.Name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM coworkers WHERE account_id=$1 AND box_id=$2`, p.AccountID, assignment.Box.ID); err != nil {
		return err
	}
	// Save the media referenced by this box before the box/task/message cascade
	// removes its links. Other boxes may still reference the same upload.
	imageRows, err := tx.QueryContext(ctx, `SELECT DISTINCT j.image_id::text
		FROM box_message_images j
		JOIN box_messages m ON m.id=j.message_id AND m.account_id=j.account_id
		JOIN box_tasks t ON t.id=m.task_id AND t.account_id=m.account_id
		WHERE j.account_id=$1 AND t.logical_box_id=$2`, p.AccountID, assignment.Box.ID)
	if err != nil {
		return err
	}
	var imageIDs []string
	for imageRows.Next() {
		var id string
		if err := imageRows.Scan(&id); err != nil {
			imageRows.Close()
			return err
		}
		imageIDs = append(imageIDs, id)
	}
	if err := imageRows.Err(); err != nil {
		imageRows.Close()
		return err
	}
	imageRows.Close()
	if assignment.Slot.ID != "" {
		result, err := tx.ExecContext(ctx, "UPDATE compute_slots SET state=CASE WHEN $5 THEN 'unhealthy' ELSE 'free' END,health=CASE WHEN $5 THEN 'unhealthy' ELSE health END,lease_owner=NULL,lease_expires_at=NULL,fencing_token=NULL,deployment_instance_id=NULL,failure_reason=CASE WHEN $5 THEN 'provider compute resource missing' ELSE NULL END,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state='draining'", p.AccountID, assignment.Slot.ID, assignment.Box.AssignmentGeneration, assignment.FencingToken, assignment.MissingCompute)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return fmt.Errorf("stale compute-slot delete fence")
		}
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state='deleting' AND ($3='' OR (assignment_generation=$4 AND fencing_token=$3)) AND ($5='' OR lease_owner=$5)", p.AccountID, assignment.Box.ID, assignment.FencingToken, assignment.Box.AssignmentGeneration, claim)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale logical-box delete fence")
	}
	if len(imageIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM run_once_images i
			WHERE i.account_id=$1 AND i.id=ANY($2::uuid[])
			AND NOT EXISTS (SELECT 1 FROM box_message_images j WHERE j.image_id=i.id)`, p.AccountID, imageIDs); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'logical_box.volume.delete','logical_box',$3,jsonb_build_object('volume_id',$4::text,'volume_name',$5::text))", p.AccountID, p.UserID, assignment.Box.ID, assignment.Box.VolumeID, assignment.Box.VolumeName); err != nil {
		return err
	}
	return tx.Commit()
}
