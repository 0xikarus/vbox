package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type logicalBoxCreation struct {
	AccountID  string
	UserID     string
	Request    v1.CreateLogicalBoxRequest
	Assignment fleetAssignment
}

var errNoCreationSlot = errors.New("no healthy free compute slot is available to initialize the workspace volume")

func (s *Store) BeginLogicalBoxCreation(ctx context.Context, p Principal, request v1.CreateLogicalBoxRequest) (logicalBoxCreation, error) {
	request.Normalize()
	if len(request.LoginProfiles) > maxBoxLoginProfiles {
		return logicalBoxCreation{}, fmt.Errorf("select at most %d login profile", maxBoxLoginProfiles)
	}
	request.DefaultAgent = selectedProfileAgent(request.DefaultAgent, request.LoginProfiles)
	var creation logicalBoxCreation
	creation.AccountID, creation.UserID, creation.Request = p.AccountID, p.UserID, request
	if err := v1.ValidateSetupScript(request.SetupScript); err != nil {
		return creation, err
	}
	if err := v1.ValidateTools(request.Tools); err != nil {
		return creation, err
	}
	if err := provider.ValidateName(request.Name); err != nil {
		return creation, err
	}
	if request.Provider == "" {
		return creation, fmt.Errorf("provider is required")
	}
	if request.DiskGiB < 1 || request.DiskGiB > 1000 {
		return creation, fmt.Errorf("diskGiB must be between 1 and 1000")
	}
	if !validAgent(request.DefaultAgent) {
		return creation, fmt.Errorf("default agent must be codex, claude, opencode, or shell")
	}
	if !v1.ValidBoxRole(request.Role) {
		return creation, fmt.Errorf("role must be worker or manager")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return creation, err
	}
	defer tx.Rollback()
	seenProfiles := map[string]bool{}
	for _, profile := range request.LoginProfiles {
		if p.Role != "owner" {
			return creation, fmt.Errorf("only an account owner may provision saved login profiles")
		}
		if seenProfiles[profile.Application] {
			return creation, fmt.Errorf("select only one profile per application")
		}
		seenProfiles[profile.Application] = true
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM login_profiles WHERE account_id=$1 AND application=$2 AND name=$3)`, p.AccountID, profile.Application, profile.Name).Scan(&exists); err != nil {
			return creation, err
		}
		if !exists {
			return creation, fmt.Errorf("selected login profile not found in this account")
		}
	}
	if err := tx.QueryRowContext(ctx, "SELECT id::text FROM logical_boxes WHERE account_id=$1 AND name=$2", p.AccountID, request.Name).Scan(new(string)); err == nil {
		return creation, fmt.Errorf("logical box %q already exists", request.Name)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return creation, err
	}
	locationFilter := ""
	queryArgs := []any{p.AccountID, request.Provider, request.ProviderCredential}
	if request.Region != "" {
		locationFilter = " AND s.region=$4"
		queryArgs = append(queryArgs, request.Region)
	}
	slot, err := scanComputeSlot(tx.QueryRowContext(ctx, computeSlotSelect+" WHERE s.account_id=$1 AND s.provider=$2 AND s.provider_credential=$3 AND s.state='free' AND s.health='healthy'"+locationFilter+" AND NOT EXISTS (SELECT 1 FROM logical_boxes assigned WHERE assigned.slot_id=s.id) ORDER BY s.ordinal FOR UPDATE OF s SKIP LOCKED LIMIT 1", queryArgs...))
	if errors.Is(err, sql.ErrNoRows) {
		return creation, errNoCreationSlot
	}
	if err != nil {
		return creation, err
	}
	// Pin the workspace to the selected slot even when the caller accepted
	// any location. Restores must not silently move a regional volume.
	request.Region = slot.Region
	creation.Request.Region = slot.Region
	id := uuid()
	generation := slot.AssignmentGeneration + 1
	fence := boxruntime.ID("create_fence_")
	leaseOwner := "create:" + id
	expires := time.Now().UTC().Add(10 * time.Minute)
	metadata, err := json.Marshal(map[string]any{"diskGiB": request.DiskGiB, "region": request.Region, "allocateWhenReady": request.ShouldAllocateWhenReady(), "allocationIdempotencyKey": request.AllocationRequestKey, "loginProfiles": request.LoginProfiles, "tools": request.Tools, "setupScript": request.SetupScript})
	if err != nil {
		return creation, err
	}
	result, err := tx.ExecContext(ctx, "UPDATE compute_slots SET state='reserved',assignment_generation=$3,lease_owner=$4,lease_expires_at=$5,fencing_token=$6,failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND state='free' AND assignment_generation=$7", p.AccountID, slot.ID, generation, leaseOwner, expires, fence, slot.AssignmentGeneration)
	if err != nil {
		return creation, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return creation, fmt.Errorf("compute slot reservation lost a concurrent race")
	}
	placeholder := "pending:" + id
	box := v1.LogicalBox{ID: id, AccountID: p.AccountID, OwnerUserID: p.UserID, Name: request.Name, Provider: request.Provider, ProviderCredential: request.ProviderCredential, DefaultAgent: request.DefaultAgent, Role: request.Role, State: v1.LogicalBoxAttaching, VolumeID: placeholder, VolumeName: "pending:" + request.Name, SlotID: slot.ID, AssignmentGeneration: generation, LeaseOwner: leaseOwner, LeaseExpiresAt: &expires, RestorationState: "creation-reserved"}
	_, err = tx.ExecContext(ctx, "INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,provider_credential,default_agent,role,state,volume_id,volume_name,slot_id,assignment_generation,lease_owner,lease_expires_at,fencing_token,restoration_state,metadata) VALUES($1,$2,$3,$4,$5,$6,$7,$17,'attaching',$8,$9,$10,$11,$12,$13,$14,$15,$16)", box.ID, p.AccountID, p.UserID, box.Name, box.Provider, box.ProviderCredential, box.DefaultAgent, box.VolumeID, box.VolumeName, slot.ID, generation, leaseOwner, expires, fence, box.RestorationState, metadata, box.Role)
	if err != nil {
		return creation, err
	}
	if err := tx.Commit(); err != nil {
		return creation, err
	}
	slot.State, slot.AssignmentGeneration, slot.LeaseOwner, slot.LeaseExpiresAt = v1.FleetSlotReserved, generation, leaseOwner, &expires
	creation.Assignment = fleetAssignment{Box: box, Slot: slot, FencingToken: fence}
	return creation, nil
}

func (s *Store) UpdateLogicalBoxCreationPhase(ctx context.Context, creation logicalBoxCreation, phase string) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE logical_boxes SET restoration_state=$5,failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state='attaching'", creation.AccountID, creation.Assignment.Box.ID, creation.Assignment.Box.AssignmentGeneration, creation.Assignment.FencingToken, phase)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale logical-box creation fence")
	}
	creation.Assignment.Box.RestorationState = phase
	return nil
}

func (s *Store) PersistLogicalBoxVolume(ctx context.Context, creation logicalBoxCreation, storage provider.Storage, phase string) error {
	if storage.ID == "" || storage.Name == "" {
		return fmt.Errorf("created volume identity is incomplete")
	}
	result, err := s.DB.ExecContext(ctx, "UPDATE logical_boxes SET volume_id=$5,volume_name=$6,restoration_state=$7,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state='attaching' AND (volume_id LIKE 'pending:%' OR volume_id=$5)", creation.AccountID, creation.Assignment.Box.ID, creation.Assignment.Box.AssignmentGeneration, creation.Assignment.FencingToken, storage.ID, storage.Name, phase)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale logical-box volume creation fence")
	}
	return nil
}

func (s *Store) CompleteLogicalBoxCreation(ctx context.Context, creation logicalBoxCreation) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE compute_slots SET state='free',lease_owner=NULL,lease_expires_at=NULL,fencing_token=NULL,deployment_instance_id=NULL,failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state IN ('reserved','draining')", creation.AccountID, creation.Assignment.Slot.ID, creation.Assignment.Box.AssignmentGeneration, creation.Assignment.FencingToken)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale compute-slot creation fence")
	}
	result, err = tx.ExecContext(ctx, "UPDATE logical_boxes SET state='hibernated',slot_id=NULL,lease_owner=NULL,lease_expires_at=NULL,fencing_token=NULL,restoration_state='saved',failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state='attaching' AND volume_id NOT LIKE 'pending:%'", creation.AccountID, creation.Assignment.Box.ID, creation.Assignment.Box.AssignmentGeneration, creation.Assignment.FencingToken)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("stale logical-box creation fence")
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'logical_box.create','logical_box',$3,jsonb_build_object('provider',$4::text))", creation.AccountID, creation.UserID, creation.Assignment.Box.ID, creation.Request.Provider); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FailLogicalBoxCreation(ctx context.Context, creation logicalBoxCreation, reason string) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE logical_boxes SET failure_reason=$5,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4", creation.AccountID, creation.Assignment.Box.ID, creation.Assignment.Box.AssignmentGeneration, creation.Assignment.FencingToken, reason); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE compute_slots SET state='draining',failure_reason=$5,updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4", creation.AccountID, creation.Assignment.Slot.ID, creation.Assignment.Box.AssignmentGeneration, creation.Assignment.FencingToken, reason); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecoverableLogicalBoxCreations(ctx context.Context) ([]logicalBoxCreation, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT b.account_id::text,b.owner_user_id::text,b.id::text,COALESCE((b.metadata->>'diskGiB')::bigint,10),COALESCE(b.metadata->>'region',''),COALESCE((b.metadata->>'allocateWhenReady')::boolean,false),COALESCE(b.metadata->>'allocationIdempotencyKey','') FROM logical_boxes b WHERE b.state='attaching' AND b.slot_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM allocation_requests r WHERE r.logical_box_id=b.id) ORDER BY b.created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct {
		accountID, userID, id, region, allocationKey string
		disk                                         int64
		allocate                                     bool
	}
	var keys []key
	for rows.Next() {
		var value key
		if err := rows.Scan(&value.accountID, &value.userID, &value.id, &value.disk, &value.region, &value.allocate, &value.allocationKey); err != nil {
			return nil, err
		}
		keys = append(keys, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	result := make([]logicalBoxCreation, 0, len(keys))
	for _, value := range keys {
		assignment, err := s.assignment(ctx, value.accountID, value.id)
		if err != nil {
			return nil, err
		}
		var raw []byte
		if err := s.DB.QueryRowContext(ctx, `SELECT metadata FROM logical_boxes WHERE account_id=$1 AND id=$2`, value.accountID, value.id).Scan(&raw); err != nil {
			return nil, err
		}
		var stored v1.CreateLogicalBoxRequest
		if err := json.Unmarshal(raw, &stored); err != nil {
			return nil, err
		}
		allocate := value.allocate
		result = append(result, logicalBoxCreation{AccountID: value.accountID, UserID: value.userID, Request: v1.CreateLogicalBoxRequest{Name: assignment.Box.Name, Provider: assignment.Box.Provider, ProviderCredential: assignment.Box.ProviderCredential, DefaultAgent: assignment.Box.DefaultAgent, Role: assignment.Box.Role, Region: value.region, DiskGiB: value.disk, AllocateWhenReady: &allocate, AllocationRequestKey: value.allocationKey}, Assignment: assignment})
		result[len(result)-1].Request.LoginProfiles = stored.LoginProfiles
		result[len(result)-1].Request.Tools = stored.Tools
		result[len(result)-1].Request.SetupScript = stored.SetupScript
	}
	return result, nil
}

type pendingAutoStart struct {
	accountID, userID, boxID, allocationKey string
}

// Creation commits the retained volume before reserving compute. If the
// controller exits between those steps, this saved intent starts only boxes
// that have never had an allocation request. Previously hibernated boxes are
// not resumed by a controller restart.
func (s *Store) PendingAutoStarts(ctx context.Context) ([]pendingAutoStart, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.account_id::text,b.owner_user_id::text,b.id::text,
 COALESCE(b.metadata->>'allocationIdempotencyKey','')
 FROM logical_boxes b WHERE b.state='hibernated' AND b.slot_id IS NULL
 AND b.restoration_state='saved' AND b.volume_id NOT LIKE 'pending:%'
 AND b.metadata->>'allocateWhenReady'='true'
 AND NOT EXISTS (SELECT 1 FROM allocation_requests r WHERE r.account_id=b.account_id AND r.logical_box_id=b.id)
 ORDER BY b.created_at,b.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pending []pendingAutoStart
	for rows.Next() {
		var item pendingAutoStart
		if err := rows.Scan(&item.accountID, &item.userID, &item.boxID, &item.allocationKey); err != nil {
			return nil, err
		}
		pending = append(pending, item)
	}
	return pending, rows.Err()
}
