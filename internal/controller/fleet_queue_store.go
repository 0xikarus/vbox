package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

func (s *Store) ReserveNextQueuedAllocation(ctx context.Context, accountID, providerName, credential string) (v1.Allocation, bool, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return v1.Allocation{}, false, err
	}
	defer tx.Rollback()
	var requestID, boxID string
	var boxGeneration int64
	err = tx.QueryRowContext(ctx, "SELECT r.id::text,b.id::text,b.assignment_generation FROM allocation_requests r JOIN logical_boxes b ON b.id=r.logical_box_id WHERE r.account_id=$1 AND r.state='queued' AND b.provider=$2 AND b.provider_credential=$3 AND b.state IN ('detached','hibernated') AND b.slot_id IS NULL AND EXISTS (SELECT 1 FROM compute_slots available WHERE available.account_id=$1 AND available.provider=$2 AND available.provider_credential=$3 AND available.state='free' AND available.health='healthy' AND (COALESCE(b.metadata->>'region','')='' OR b.metadata->>'region'=available.region) AND NOT EXISTS (SELECT 1 FROM logical_boxes assigned WHERE assigned.slot_id=available.id)) ORDER BY r.created_at,r.id FOR UPDATE OF r,b SKIP LOCKED LIMIT 1", accountID, providerName, credential).Scan(&requestID, &boxID, &boxGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return v1.Allocation{}, false, nil
	}
	if err != nil {
		return v1.Allocation{}, false, err
	}
	slot, err := scanComputeSlot(tx.QueryRowContext(ctx, computeSlotSelect+" WHERE s.account_id=$1 AND s.provider=$2 AND s.provider_credential=$3 AND s.state='free' AND s.health='healthy' AND EXISTS (SELECT 1 FROM logical_boxes location WHERE location.id=$4 AND location.account_id=$1 AND (COALESCE(location.metadata->>'region','')='' OR location.metadata->>'region'=s.region)) AND NOT EXISTS (SELECT 1 FROM logical_boxes assigned WHERE assigned.slot_id=s.id) ORDER BY s.ordinal FOR UPDATE OF s SKIP LOCKED LIMIT 1", accountID, providerName, credential, boxID))
	if errors.Is(err, sql.ErrNoRows) {
		return v1.Allocation{}, false, nil
	}
	if err != nil {
		return v1.Allocation{}, false, err
	}
	generation := slot.AssignmentGeneration
	if boxGeneration > generation {
		generation = boxGeneration
	}
	generation++
	fence := boxruntime.ID("fence_")
	leaseOwner := "queue:" + requestID
	expires := time.Now().UTC().Add(2 * time.Minute)
	result, err := tx.ExecContext(ctx, "UPDATE compute_slots SET state='reserved',assignment_generation=$3,lease_owner=$4,lease_expires_at=$5,fencing_token=$6,failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND state='free' AND assignment_generation=$7", accountID, slot.ID, generation, leaseOwner, expires, fence, slot.AssignmentGeneration)
	if err != nil {
		return v1.Allocation{}, false, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return v1.Allocation{}, false, fmt.Errorf("queued compute-slot reservation lost a race")
	}
	result, err = tx.ExecContext(ctx, "UPDATE logical_boxes SET state='reserved',slot_id=$3,assignment_generation=$4,lease_owner=$5,lease_expires_at=$6,fencing_token=$7,failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND slot_id IS NULL AND state IN ('detached','hibernated')", accountID, boxID, slot.ID, generation, leaseOwner, expires, fence)
	if err != nil {
		return v1.Allocation{}, false, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return v1.Allocation{}, false, fmt.Errorf("queued logical-box reservation lost a race")
	}
	result, err = tx.ExecContext(ctx, "UPDATE allocation_requests SET state='reserved',slot_id=$3,assignment_generation=$4,fencing_token=$5,phase='reserved',failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND state='queued'", accountID, requestID, slot.ID, generation, fence)
	if err != nil {
		return v1.Allocation{}, false, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return v1.Allocation{}, false, fmt.Errorf("queued allocation reservation lost a race")
	}
	if err := tx.Commit(); err != nil {
		return v1.Allocation{}, false, err
	}
	allocation, err := s.Allocation(ctx, accountID, requestID)
	return allocation, err == nil, err
}
