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
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

func (s *Store) FleetConfig(ctx context.Context, accountID, providerName, credential string) (v1.FleetConfig, error) {
	config := v1.FleetConfig{Provider: providerName, ProviderCredential: credential}
	if providerName == "" {
		return config, fmt.Errorf("provider is required")
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO fleet_settings(account_id,provider,provider_credential,compute_box_slots) VALUES($1,$2,$3,$4) ON CONFLICT(account_id,provider,provider_credential) DO NOTHING`, accountID, providerName, credential, v1.DefaultComputeBoxSlots)
	if err != nil {
		return config, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT provider,provider_credential,compute_box_slots,updated_at,region FROM fleet_settings WHERE account_id=$1 AND provider=$2 AND provider_credential=$3`, accountID, providerName, credential).Scan(&config.Provider, &config.ProviderCredential, &config.ComputeBoxSlots, &config.UpdatedAt, &config.Region)
	return config, err
}

func (s *Store) SetFleetConfig(ctx context.Context, p Principal, requested v1.FleetConfig) (v1.FleetConfig, error) {
	if err := requested.Validate(); err != nil {
		return v1.FleetConfig{}, err
	}
	var config v1.FleetConfig
	err := s.DB.QueryRowContext(ctx, `INSERT INTO fleet_settings(account_id,provider,provider_credential,compute_box_slots) VALUES($1,$2,$3,$4) ON CONFLICT(account_id,provider,provider_credential) DO UPDATE SET compute_box_slots=excluded.compute_box_slots,updated_at=now() RETURNING provider,provider_credential,compute_box_slots,updated_at`, p.AccountID, requested.Provider, requested.ProviderCredential, requested.ComputeBoxSlots).Scan(&config.Provider, &config.ProviderCredential, &config.ComputeBoxSlots, &config.UpdatedAt)
	if err != nil {
		return config, err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'fleet.slots.set','fleet',$3,jsonb_build_object('compute_box_slots',$4::integer))`, p.AccountID, p.UserID, requested.Provider+":"+requested.ProviderCredential, requested.ComputeBoxSlots)
	return config, err
}

func (s *Store) UpsertComputeSlot(ctx context.Context, accountID string, slot v1.ComputeSlot) (v1.ComputeSlot, error) {
	if slot.Provider == "" || slot.Ordinal <= 0 {
		return slot, fmt.Errorf("slot provider and positive ordinal are required")
	}
	if slot.ID == "" {
		slot.ID = uuid()
	}
	if slot.State == "" {
		slot.State = v1.FleetSlotStarting
	}
	if slot.Health == "" {
		slot.Health = "unknown"
	}
	err := s.DB.QueryRowContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,provider_credential,ordinal,state,service_id,service_name,deployment_instance_id,region,image,image_version,health,failure_reason) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),$13,NULLIF($14,'')) ON CONFLICT(account_id,provider,provider_credential,ordinal) DO UPDATE SET state=excluded.state,service_id=COALESCE(excluded.service_id,compute_slots.service_id),service_name=COALESCE(excluded.service_name,compute_slots.service_name),deployment_instance_id=excluded.deployment_instance_id,region=COALESCE(excluded.region,compute_slots.region),image=COALESCE(excluded.image,compute_slots.image),image_version=COALESCE(excluded.image_version,compute_slots.image_version),health=excluded.health,failure_reason=excluded.failure_reason,updated_at=now() WHERE compute_slots.state<>'deprovisioning' RETURNING id::text,created_at,updated_at`, slot.ID, accountID, slot.Provider, slot.ProviderCredential, slot.Ordinal, slot.State, slot.ServiceID, slot.ServiceName, slot.DeploymentInstanceID, slot.Region, slot.Image, slot.ImageVersion, slot.Health, slot.FailureReason).Scan(&slot.ID, &slot.CreatedAt, &slot.UpdatedAt)
	slot.AccountID = accountID
	return slot, err
}

func (s *Store) SetComputeSlotObservedImage(ctx context.Context, accountID, slotID, image, digest string) error {
	if accountID == "" || slotID == "" || strings.TrimSpace(image) == "" {
		return fmt.Errorf("account, slot, and observed image are required")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE compute_slots SET image=$3,image_version=NULLIF($4,''),updated_at=now() WHERE account_id=$1 AND id=$2`, accountID, slotID, image, digest)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("compute slot not found")
	}
	return nil
}

func (s *Store) UpsertLogicalBox(ctx context.Context, p Principal, box v1.LogicalBox) (v1.LogicalBox, error) {
	if box.Name == "" || box.Provider == "" || box.VolumeID == "" || box.VolumeName == "" {
		return box, fmt.Errorf("logical box name, provider, volume ID, and volume name are required")
	}
	if box.ID == "" {
		box.ID = uuid()
	}
	if box.OwnerUserID == "" {
		box.OwnerUserID = p.UserID
	}
	if box.State == "" {
		box.State = v1.LogicalBoxDetached
	}
	box.DefaultAgent = strings.ToLower(strings.TrimSpace(box.DefaultAgent))
	if box.DefaultAgent == "" {
		box.DefaultAgent = "claude"
	}
	if !validAgent(box.DefaultAgent) {
		return box, fmt.Errorf("default agent must be codex, claude, opencode, or shell")
	}
	err := s.DB.QueryRowContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,provider_credential,default_agent,state,volume_id,volume_name) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10 WHERE EXISTS (SELECT 1 FROM provider_credentials pc WHERE pc.account_id=$2 AND pc.provider=$5 AND pc.name=$6 AND NOT pc.deleting FOR SHARE) ON CONFLICT(account_id,name) DO UPDATE SET updated_at=now() WHERE logical_boxes.volume_id=excluded.volume_id AND logical_boxes.provider=excluded.provider AND logical_boxes.owner_user_id=excluded.owner_user_id RETURNING id::text,owner_user_id::text,state,created_at,updated_at`, box.ID, p.AccountID, box.OwnerUserID, box.Name, box.Provider, box.ProviderCredential, box.DefaultAgent, box.State, box.VolumeID, box.VolumeName).Scan(&box.ID, &box.OwnerUserID, &box.State, &box.CreatedAt, &box.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return box, fmt.Errorf("logical box ownership or volume identity does not match the existing record")
	}
	box.AccountID = p.AccountID
	return box, err
}

func scanComputeSlot(scanner interface{ Scan(...any) error }) (v1.ComputeSlot, error) {
	var slot v1.ComputeSlot
	var lease sql.NullTime
	err := scanner.Scan(
		&slot.ID, &slot.AccountID, &slot.Provider, &slot.ProviderCredential, &slot.Ordinal,
		&slot.State, &slot.ServiceID, &slot.ServiceName, &slot.DeploymentInstanceID,
		&slot.LogicalBoxID, &slot.LogicalBoxName, &slot.Region, &slot.Image,
		&slot.ImageVersion, &slot.Health, &slot.AssignmentGeneration, &slot.LeaseOwner,
		&lease, &slot.FailureReason, &slot.CreatedAt, &slot.UpdatedAt,
	)
	if lease.Valid {
		slot.LeaseExpiresAt = &lease.Time
	}
	return slot, err
}

const computeSlotSelect = `SELECT s.id::text,s.account_id::text,s.provider,s.provider_credential,s.ordinal,s.state,COALESCE(s.service_id,''),COALESCE(s.service_name,''),COALESCE(s.deployment_instance_id,''),COALESCE(b.id::text,''),COALESCE(b.name,''),COALESCE(s.region,''),COALESCE(s.image,''),COALESCE(s.image_version,''),s.health,s.assignment_generation,COALESCE(s.lease_owner,''),s.lease_expires_at,COALESCE(s.failure_reason,''),s.created_at,s.updated_at FROM compute_slots s LEFT JOIN logical_boxes b ON b.slot_id=s.id`

func scanLogicalBox(scanner interface{ Scan(...any) error }) (v1.LogicalBox, error) {
	var box v1.LogicalBox
	var lease sql.NullTime
	var tools []byte
	var roles []byte
	err := scanner.Scan(
		&box.ID, &box.AccountID, &box.OwnerUserID, &box.Name, &box.Provider,
		&box.ProviderCredential, &box.DefaultAgent, &roles, &box.State, &box.VolumeID, &box.VolumeName,
		&box.SlotID, &box.AssignmentGeneration, &box.LeaseOwner, &lease,
		&box.RestorationState, &box.FailureReason, &box.CreatedAt, &box.UpdatedAt,
		&tools,
	)
	if err == nil {
		if len(tools) > 0 && tools[0] == '{' {
			var saved struct {
				Tools    []string `json:"tools"`
				LastStop string   `json:"lastStop"`
			}
			err = json.Unmarshal(tools, &saved)
			box.Tools = saved.Tools
			box.LastStopReason = saved.LastStop
		} else {
			err = json.Unmarshal(tools, &box.Tools)
		}
	}
	if err == nil {
		if unmarshalErr := json.Unmarshal(roles, &box.Roles); unmarshalErr != nil {
			// Old sqlmock fixtures supplied the removed worker/manager scalar in
			// this position. It carries no permissions in the native role model.
			box.Roles = []v1.AgentRoleSummary{}
		}
	}
	if lease.Valid {
		box.LeaseExpiresAt = &lease.Time
	}
	return box, err
}

const logicalBoxSelect = `SELECT id::text,account_id::text,owner_user_id::text,name,provider,provider_credential,default_agent,COALESCE((SELECT jsonb_agg(jsonb_build_object('id',r.id::text,'name',r.name) ORDER BY lower(r.name),r.id) FROM box_role_assignments a JOIN agent_roles r ON r.id=a.role_id AND r.account_id=a.account_id WHERE a.account_id=logical_boxes.account_id AND a.box_id=logical_boxes.id),'[]'::jsonb),state,volume_id,volume_name,COALESCE(slot_id::text,''),assignment_generation,COALESCE(lease_owner,''),lease_expires_at,COALESCE(restoration_state,''),COALESCE(failure_reason,''),created_at,updated_at,jsonb_build_object('tools',COALESCE(metadata->'tools','[]'::jsonb),'lastStop',COALESCE(metadata->>'lastStop','')) FROM logical_boxes`

func (s *Store) FleetStatus(ctx context.Context, accountID, providerName, credential string) (v1.FleetStatus, error) {
	config, err := s.FleetConfig(ctx, accountID, providerName, credential)
	if err != nil {
		return v1.FleetStatus{}, err
	}
	status := v1.FleetStatus{Provider: providerName, ProviderCredential: credential, DesiredSlots: config.ComputeBoxSlots, Slots: []v1.ComputeSlot{}, DetachedLogicalBoxes: []v1.LogicalBox{}}
	rows, err := s.DB.QueryContext(ctx, computeSlotSelect+` WHERE s.account_id=$1 AND s.provider=$2 AND s.provider_credential=$3 ORDER BY s.ordinal`, accountID, providerName, credential)
	if err != nil {
		return status, err
	}
	for rows.Next() {
		slot, scanErr := scanComputeSlot(rows)
		if scanErr != nil {
			rows.Close()
			return status, scanErr
		}
		status.Slots = append(status.Slots, slot)
		switch slot.State {
		case v1.FleetSlotFree:
			status.FreeSlots++
		case v1.FleetSlotReserved, v1.FleetSlotOccupied:
			status.OccupiedSlots++
		case v1.FleetSlotStarting:
			status.StartingSlots++
		case v1.FleetSlotDraining:
			status.DrainingSlots++
		case v1.FleetSlotUnhealthy:
			status.UnhealthySlots++
		case v1.FleetSlotStopped:
			status.StoppedSlots++
		}
	}
	if err := rows.Close(); err != nil {
		return status, err
	}
	status.ActualSlots = len(status.Slots)
	boxRows, err := s.DB.QueryContext(ctx, logicalBoxSelect+` WHERE account_id=$1 AND provider=$2 AND provider_credential=$3 AND slot_id IS NULL AND state IN ('detached','hibernated') ORDER BY name`, accountID, providerName, credential)
	if err != nil {
		return status, err
	}
	for boxRows.Next() {
		box, scanErr := scanLogicalBox(boxRows)
		if scanErr != nil {
			boxRows.Close()
			return status, scanErr
		}
		status.DetachedLogicalBoxes = append(status.DetachedLogicalBoxes, box)
	}
	if err := boxRows.Close(); err != nil {
		return status, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM allocation_requests r JOIN logical_boxes b ON b.id=r.logical_box_id WHERE r.account_id=$1 AND b.provider=$2 AND b.provider_credential=$3 AND r.state='queued'`, accountID, providerName, credential).Scan(&status.PendingAllocations)
	return status, err
}

func (s *Store) allocationByKey(ctx context.Context, accountID, idempotency string) (v1.Allocation, error) {
	var allocation v1.Allocation
	var lease sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT r.id::text,r.idempotency_key,r.state,r.logical_box_id::text,b.name,COALESCE(r.slot_id::text,''),COALESCE(s.service_id,''),COALESCE(r.assignment_generation,0),COALESCE(r.fencing_token,''),COALESCE(b.lease_owner,''),b.lease_expires_at,COALESCE(r.phase,''),r.retry_count,COALESCE(r.failure_reason,''),r.created_at,r.updated_at FROM allocation_requests r JOIN logical_boxes b ON b.id=r.logical_box_id LEFT JOIN compute_slots s ON s.id=r.slot_id WHERE r.account_id=$1 AND r.idempotency_key=$2`, accountID, idempotency).Scan(&allocation.RequestID, &allocation.IdempotencyKey, &allocation.State, &allocation.LogicalBoxID, &allocation.LogicalBoxName, &allocation.SlotID, &allocation.ServiceID, &allocation.AssignmentGeneration, &allocation.FencingToken, &allocation.LeaseOwner, &lease, &allocation.Phase, &allocation.RetryCount, &allocation.FailureReason, &allocation.CreatedAt, &allocation.UpdatedAt)
	if lease.Valid {
		allocation.LeaseExpiresAt = &lease.Time
	}
	if err == nil && allocation.State == "queued" {
		_ = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM allocation_requests WHERE account_id=$1 AND state='queued' AND (created_at,id)<($2,$3::uuid)`, accountID, allocation.CreatedAt, allocation.RequestID).Scan(&allocation.QueuePosition)
		allocation.QueuePosition++
	}
	return allocation, err
}

func (s *Store) ReserveAllocation(ctx context.Context, p Principal, logicalBox, idempotency, leaseOwner string, leaseDuration time.Duration) (v1.Allocation, error) {
	return s.reserveAllocation(ctx, p, logicalBox, idempotency, leaseOwner, leaseDuration, "")
}

// ReserveAllocationOnSlot keeps new-box placement on the worker selected at
// creation. Unlike automatic allocation, it does not queue for another slot.
func (s *Store) ReserveAllocationOnSlot(ctx context.Context, p Principal, logicalBox, idempotency, leaseOwner string, leaseDuration time.Duration, slotID string) (v1.Allocation, error) {
	return s.reserveAllocation(ctx, p, logicalBox, idempotency, leaseOwner, leaseDuration, slotID)
}

func (s *Store) reserveAllocation(ctx context.Context, p Principal, logicalBox, idempotency, leaseOwner string, leaseDuration time.Duration, preferredSlotID string) (v1.Allocation, error) {
	if idempotency == "" {
		return v1.Allocation{}, fmt.Errorf("Idempotency-Key is required")
	}
	if existing, err := s.allocationByKey(ctx, p.AccountID, idempotency); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return v1.Allocation{}, err
	}
	if leaseOwner == "" {
		leaseOwner = "user:" + p.UserID
	}
	if leaseDuration <= 0 {
		leaseDuration = 2 * time.Minute
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return v1.Allocation{}, err
	}
	defer tx.Rollback()
	box, err := scanLogicalBox(tx.QueryRowContext(ctx, logicalBoxSelect+` WHERE account_id=$1 AND (id::text=$2 OR name=$2) FOR UPDATE`, p.AccountID, logicalBox))
	if errors.Is(err, sql.ErrNoRows) {
		return v1.Allocation{}, fmt.Errorf("logical box not found")
	}
	if err != nil {
		return v1.Allocation{}, err
	}
	if box.OwnerUserID != p.UserID && p.Role != "owner" {
		return v1.Allocation{}, fmt.Errorf("logical box belongs to another user")
	}
	// Opening the same logical box from another terminal while its allocation is
	// already progressing must follow that fenced request. Creating a second
	// request would either race the first activation or reject a perfectly valid
	// reconnect with the misleading "not detached" error.
	if box.State == v1.LogicalBoxReserved || box.State == v1.LogicalBoxAttaching {
		allocation, activeErr := scanAllocation(tx.QueryRowContext(ctx, allocationSelect+` WHERE r.account_id=$1 AND r.logical_box_id=$2 AND r.assignment_generation=$3 AND r.state IN ('reserved','attaching') ORDER BY r.created_at DESC LIMIT 1`, p.AccountID, box.ID, box.AssignmentGeneration))
		if errors.Is(activeErr, sql.ErrNoRows) {
			return allocation, fmt.Errorf("logical box %q is %s but has no matching active allocation", box.Name, box.State)
		}
		if activeErr != nil {
			return allocation, activeErr
		}
		if err := tx.Commit(); err != nil {
			return allocation, err
		}
		return allocation, nil
	}
	if box.State == v1.LogicalBoxHibernating || box.State == v1.LogicalBoxHibernated || box.State == v1.LogicalBoxDetached {
		// Reuse pending intent even after detach, before the scheduler has
		// claimed it; otherwise a second opener leaves a stale resume queued.
		allocation, queuedErr := scanAllocation(tx.QueryRowContext(ctx, allocationSelect+` WHERE r.account_id=$1 AND r.logical_box_id=$2 AND r.state='queued' ORDER BY r.created_at LIMIT 1`, p.AccountID, box.ID))
		if queuedErr == nil {
			if err := tx.Commit(); err != nil {
				return allocation, err
			}
			return allocation, nil
		}
		if !errors.Is(queuedErr, sql.ErrNoRows) {
			return allocation, queuedErr
		}
	}
	if box.State == v1.LogicalBoxHibernating {
		// Never race a workspace unmount. The scheduler reserves only
		// detached, slot-free boxes.
		_, err = tx.ExecContext(ctx, `INSERT INTO allocation_requests(id,account_id,logical_box_id,state,idempotency_key,requested_by,phase) VALUES($1,$2,$3,'queued',$4,$5,'waiting-for-hibernate')`, uuid(), p.AccountID, box.ID, idempotency, p.UserID)
		if err != nil {
			return v1.Allocation{}, err
		}
		if err = tx.Commit(); err != nil {
			return v1.Allocation{}, err
		}
		return s.allocationByKey(ctx, p.AccountID, idempotency)
	}
	if box.State != v1.LogicalBoxDetached && box.State != v1.LogicalBoxHibernated {
		return v1.Allocation{}, fmt.Errorf("logical box %q is %s, not detached", box.Name, box.State)
	}
	requestID := uuid()
	slotFilter := ""
	slotArgs := []any{p.AccountID, box.Provider, box.ProviderCredential, box.ID}
	if preferredSlotID != "" {
		slotFilter = " AND s.id::text=$5"
		slotArgs = append(slotArgs, preferredSlotID)
	}
	row := tx.QueryRowContext(ctx, computeSlotSelect+` WHERE s.account_id=$1 AND s.provider=$2 AND s.provider_credential=$3 AND s.state='free' AND s.health='healthy' AND EXISTS (SELECT 1 FROM provider_credentials pc WHERE pc.account_id=s.account_id AND pc.provider=s.provider AND pc.name=s.provider_credential AND NOT pc.deleting FOR SHARE) AND EXISTS (SELECT 1 FROM logical_boxes location WHERE location.id=$4 AND location.account_id=$1 AND (COALESCE(location.metadata->>'region','')='' OR location.metadata->>'region'=s.region)) AND NOT EXISTS (SELECT 1 FROM logical_boxes assigned WHERE assigned.slot_id=s.id)`+slotFilter+` ORDER BY s.ordinal FOR UPDATE OF s SKIP LOCKED LIMIT 1`, slotArgs...)
	slot, slotErr := scanComputeSlot(row)
	if errors.Is(slotErr, sql.ErrNoRows) {
		if preferredSlotID != "" {
			return v1.Allocation{}, fmt.Errorf("selected worker slot is no longer available")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO allocation_requests(id,account_id,logical_box_id,state,idempotency_key,requested_by,phase) VALUES($1,$2,$3,'queued',$4,$5,'waiting-for-capacity') ON CONFLICT(account_id,idempotency_key) DO NOTHING`, requestID, p.AccountID, box.ID, idempotency, p.UserID)
		if err != nil {
			return v1.Allocation{}, err
		}
		if err := tx.Commit(); err != nil {
			return v1.Allocation{}, err
		}
		return s.allocationByKey(ctx, p.AccountID, idempotency)
	}
	if slotErr != nil {
		return v1.Allocation{}, slotErr
	}
	generation := slot.AssignmentGeneration
	if box.AssignmentGeneration > generation {
		generation = box.AssignmentGeneration
	}
	generation++
	fence := boxruntime.ID("fence_")
	expires := time.Now().UTC().Add(leaseDuration)
	result, err := tx.ExecContext(ctx, `UPDATE compute_slots SET state='reserved',assignment_generation=$3,lease_owner=$4,lease_expires_at=$5,fencing_token=$6,updated_at=now() WHERE id=$1 AND account_id=$2 AND state='free' AND assignment_generation=$7`, slot.ID, p.AccountID, generation, leaseOwner, expires, fence, slot.AssignmentGeneration)
	if err != nil {
		return v1.Allocation{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return v1.Allocation{}, fmt.Errorf("compute slot reservation lost a concurrent race")
	}
	result, err = tx.ExecContext(ctx, `UPDATE logical_boxes SET state='reserved',slot_id=$3,assignment_generation=$4,lease_owner=$5,lease_expires_at=$6,fencing_token=$7,failure_reason=NULL,updated_at=now() WHERE id=$1 AND account_id=$2 AND slot_id IS NULL AND state IN ('detached','hibernated')`, box.ID, p.AccountID, slot.ID, generation, leaseOwner, expires, fence)
	if err != nil {
		return v1.Allocation{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return v1.Allocation{}, fmt.Errorf("logical box reservation lost a concurrent race")
	}
	var created, updated time.Time
	err = tx.QueryRowContext(ctx, `INSERT INTO allocation_requests(id,account_id,logical_box_id,state,idempotency_key,requested_by,slot_id,assignment_generation,fencing_token,phase) VALUES($1,$2,$3,'reserved',$4,$5,$6,$7,$8,'reserved') ON CONFLICT(account_id,idempotency_key) DO NOTHING RETURNING created_at,updated_at`, requestID, p.AccountID, box.ID, idempotency, p.UserID, slot.ID, generation, fence).Scan(&created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return v1.Allocation{}, fmt.Errorf("idempotency key was concurrently claimed; retry the request")
	}
	if err != nil {
		return v1.Allocation{}, err
	}
	if err := tx.Commit(); err != nil {
		return v1.Allocation{}, err
	}
	return v1.Allocation{RequestID: requestID, IdempotencyKey: idempotency, State: "reserved", LogicalBoxID: box.ID, LogicalBoxName: box.Name, SlotID: slot.ID, ServiceID: slot.ServiceID, AssignmentGeneration: generation, FencingToken: fence, LeaseOwner: leaseOwner, LeaseExpiresAt: &expires, CreatedAt: created, UpdatedAt: updated}, nil
}

func (s *Store) CompleteAssignment(ctx context.Context, accountID, logicalBoxID string, generation int64, fencingToken, deploymentInstanceID string) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var slotID string
	err = tx.QueryRowContext(ctx, `SELECT slot_id::text FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state IN ('reserved','attaching') AND assignment_generation=$3 AND fencing_token=$4 FOR UPDATE`, accountID, logicalBoxID, generation, fencingToken).Scan(&slotID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("stale assignment fencing token")
	}
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE compute_slots SET state='occupied',deployment_instance_id=NULLIF($4,''),updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$5 AND state IN ('reserved','starting')`, accountID, slotID, generation, deploymentInstanceID, fencingToken)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale compute-slot fencing token")
	}
	result, err = tx.ExecContext(ctx, `UPDATE logical_boxes SET state='running',restoration_state='restored',updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state IN ('reserved','attaching')`, accountID, logicalBoxID, generation, fencingToken)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale logical-box fencing token")
	}
	result, err = tx.ExecContext(ctx, `UPDATE allocation_requests SET state='ready',phase='ready',failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND logical_box_id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state IN ('reserved','attaching')`, accountID, logicalBoxID, generation, fencingToken)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale allocation fencing token")
	}
	return tx.Commit()
}

func (s *Store) ReleaseAssignment(ctx context.Context, accountID, logicalBoxID string, generation int64, fencingToken string, finalState v1.LogicalBoxState) error {
	if finalState != v1.LogicalBoxDetached && finalState != v1.LogicalBoxHibernated {
		return fmt.Errorf("release final state must be detached or hibernated")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var slotID string
	err = tx.QueryRowContext(ctx, `SELECT slot_id::text FROM logical_boxes WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4 FOR UPDATE`, accountID, logicalBoxID, generation, fencingToken).Scan(&slotID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("stale assignment fencing token")
	}
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE compute_slots SET state='free',lease_owner=NULL,lease_expires_at=NULL,fencing_token=NULL,deployment_instance_id=NULL,failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4`, accountID, slotID, generation, fencingToken)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale compute-slot fencing token")
	}
	result, err = tx.ExecContext(ctx, `UPDATE logical_boxes SET state=$5,restoration_state='saved',slot_id=NULL,lease_owner=NULL,lease_expires_at=NULL,fencing_token=NULL,failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4`, accountID, logicalBoxID, generation, fencingToken, finalState)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale logical-box fencing token")
	}
	if finalState == v1.LogicalBoxHibernated {
		if err := appendBoxEvent(ctx, tx, accountID, logicalBoxID, "hibernated · workspace saved", fmt.Sprintf("hibernate:%s:%d", logicalBoxID, generation)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
