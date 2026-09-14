package controller

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

// RecordWorkerObservation never extends a lease or changes lifecycle/task state.
// Receipt time is controller-owned; an agent timestamp cannot make data fresh.
func (s *Store) RecordWorkerObservation(ctx context.Context, w DirectWorker, owner string, observation workerprotocol.Observation) error {
	if observation.Binding.AccountID != w.AccountID || observation.Binding.SlotID != w.SlotID || observation.Binding.Incarnation != w.Incarnation || observation.Sequence == 0 || observation.Sequence > math.MaxInt64 {
		return errWorkerIdentity
	}
	if len(observation.Runtime) > 0 {
		var inventory v1.SessionInventory
		if len(observation.Runtime) > 32*1024 || json.Unmarshal(observation.Runtime, &inventory) != nil ||
			inventory.Assignment != observation.Binding.Assignment || inventory.Sessions == nil || len(inventory.Sessions) > 64 {
			return errors.New("invalid runtime observation")
		}
	}
	current, err := s.WorkerBinding(ctx, w)
	if err != nil {
		return nil
	} // No authoritative assignment: retain prior evidence.
	if current != observation.Binding {
		return nil
	} // In-flight snapshot from old binding.
	data, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE direct_workers SET observation=$5::jsonb,observed_at=now(),observation_epoch=$3,observation_sequence=$6
 WHERE id=$1 AND account_id=$2 AND connection_epoch=$3 AND connection_owner=$4
 AND incarnation=$7 AND revoked_at IS NULL AND connection_expires_at>now()
 AND (observation_epoch<$3 OR observation_sequence<$6)`, w.ID, w.AccountID, w.Epoch, owner, data, int64(observation.Sequence), w.Incarnation)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errWorkerIdentity
	}
	return nil
}
