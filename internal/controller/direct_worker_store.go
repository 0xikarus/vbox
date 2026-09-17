package controller

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/0xikarus/vmbox-service/internal/secrets"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

var errWorkerIdentity = errors.New("worker identity unavailable or expired")

// errControllerLeaseLost reports that another active controller owns the
// single-controller lease; this process must stop serving worker connections.
var errControllerLeaseLost = errors.New("direct workers require a single active controller")

type DirectWorker struct {
	ID          string `json:"id"`
	AccountID   string `json:"accountId"`
	SlotID      string `json:"slotId"`
	ServiceID   string `json:"serviceId"`
	Incarnation string `json:"incarnation"`
	Epoch       int64  `json:"epoch"`
	Enabled     bool   `json:"enabled"`
}

// IssueWorkerEnrollment is called only by an owner-authorized bootstrap path.
// It cannot rotate a credential or displace an already registered worker.
func (s *Store) IssueWorkerEnrollment(ctx context.Context, accountID, slotID string) (DirectWorker, string, error) {
	token, err := secretToken()
	if err != nil {
		return DirectWorker{}, "", err
	}
	worker := DirectWorker{ID: uuid(), AccountID: accountID, SlotID: slotID}
	err = s.DB.QueryRowContext(ctx, `INSERT INTO direct_workers(id,account_id,slot_id,enrollment_hash,enrollment_expires_at)
 SELECT $1,account_id,id,$4,now()+interval '15 minutes' FROM compute_slots
 WHERE id=$2 AND account_id=$3 AND service_id IS NOT NULL AND provider='railway'
 RETURNING (SELECT service_id FROM compute_slots WHERE id=$2)`, worker.ID, slotID, accountID, secrets.TokenHash(token)).Scan(&worker.ServiceID)
	if err != nil {
		return DirectWorker{}, "", fmt.Errorf("worker enrollment could not be issued: %w", err)
	}
	return worker, token, nil
}

func validWorkerSecret(token string) bool {
	data, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(data) == 32
}

// DiscardUnusedWorkerEnrollment is only safe before the bootstrap mutation has
// been attempted. A possibly installed agent keeps its enrollment for recovery.
func (s *Store) DiscardUnusedWorkerEnrollment(ctx context.Context, worker DirectWorker, token string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM direct_workers WHERE id=$1 AND account_id=$2 AND slot_id=$3 AND enrollment_hash=$4 AND credential_hash IS NULL AND NOT transport_enabled`, worker.ID, worker.AccountID, worker.SlotID, secrets.TokenHash(token))
	return err
}

// The agent saves its proposed credential before exchange. If the response is
// lost it can authenticate with that credential, rather than reuse enrollment.
func (s *Store) ExchangeWorkerEnrollment(ctx context.Context, enrollment, credential string) (DirectWorker, error) {
	var worker DirectWorker
	if !validWorkerSecret(enrollment) || !validWorkerSecret(credential) || enrollment == credential {
		return worker, errWorkerIdentity
	}
	err := s.DB.QueryRowContext(ctx, `UPDATE direct_workers SET credential_hash=$2,
 enrollment_hash=NULL,enrollment_expires_at=NULL,enrolled_at=now()
 WHERE enrollment_hash=$1 AND enrollment_expires_at>now() AND credential_hash IS NULL AND revoked_at IS NULL
 RETURNING id::text,account_id::text,slot_id::text`, secrets.TokenHash(enrollment), secrets.TokenHash(credential)).Scan(&worker.ID, &worker.AccountID, &worker.SlotID)
	if errors.Is(err, sql.ErrNoRows) {
		return DirectWorker{}, errWorkerIdentity
	}
	return worker, err
}

func (s *Store) AuthenticateWorker(ctx context.Context, credential string) (DirectWorker, error) {
	var worker DirectWorker
	if !validWorkerSecret(credential) {
		return worker, errWorkerIdentity
	}
	err := s.DB.QueryRowContext(ctx, `SELECT w.id::text,w.account_id::text,w.slot_id::text,COALESCE(c.service_id,''),w.incarnation,w.connection_epoch,w.transport_enabled
 FROM direct_workers w JOIN compute_slots c ON c.id=w.slot_id AND c.account_id=w.account_id
 WHERE w.credential_hash=$1 AND w.revoked_at IS NULL`, secrets.TokenHash(credential)).Scan(&worker.ID, &worker.AccountID, &worker.SlotID, &worker.ServiceID, &worker.Incarnation, &worker.Epoch, &worker.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return DirectWorker{}, errWorkerIdentity
	}
	return worker, err
}

// ClaimWorkerConnection advances the epoch atomically. The caller must close any
// old local connection and require this epoch for every operation and renewal.
func (s *Store) ClaimWorkerConnection(ctx context.Context, credential, incarnation, owner string) (DirectWorker, error) {
	var worker DirectWorker
	if !validWorkerSecret(credential) || !validWorkerSecret(incarnation) || !validWorkerSecret(owner) {
		return worker, errWorkerIdentity
	}
	err := s.DB.QueryRowContext(ctx, `UPDATE direct_workers w SET incarnation=$2,connection_owner=$3,
 connection_epoch=connection_epoch+1,connection_expires_at=now()+interval '60 seconds'
 FROM compute_slots c WHERE w.credential_hash=$1 AND w.revoked_at IS NULL
 AND c.id=w.slot_id AND c.account_id=w.account_id
 RETURNING w.id::text,w.account_id::text,w.slot_id::text,COALESCE(c.service_id,''),w.incarnation,w.connection_epoch,w.transport_enabled`, secrets.TokenHash(credential), incarnation, owner).Scan(&worker.ID, &worker.AccountID, &worker.SlotID, &worker.ServiceID, &worker.Incarnation, &worker.Epoch, &worker.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return DirectWorker{}, errWorkerIdentity
	}
	return worker, err
}

func (s *Store) RenewWorkerConnection(ctx context.Context, worker DirectWorker, owner string, observation json.RawMessage) error {
	if len(observation) > 64*1024 || (len(observation) > 0 && !validJSONObject(observation, false)) {
		return errors.New("invalid worker observation")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE direct_workers SET connection_expires_at=now()+interval '60 seconds',observation=COALESCE($5::jsonb,observation),observed_at=CASE WHEN $5::jsonb IS NULL THEN observed_at ELSE now() END
 WHERE id=$1 AND account_id=$2 AND connection_epoch=$3 AND connection_owner=$4
 AND incarnation=$6 AND revoked_at IS NULL AND connection_expires_at>now()`, worker.ID, worker.AccountID, worker.Epoch, owner, observation, worker.Incarnation)
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

func (s *Store) ReleaseWorkerConnection(ctx context.Context, worker DirectWorker, owner string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE direct_workers SET connection_expires_at=NULL WHERE id=$1 AND account_id=$2 AND connection_epoch=$3 AND connection_owner=$4`, worker.ID, worker.AccountID, worker.Epoch, owner)
	return err
}

func (s *Store) RevokeWorker(ctx context.Context, accountID, workerID string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE direct_workers SET revoked_at=now(),connection_expires_at=NULL,
 connection_epoch=connection_epoch+1,enrollment_hash=NULL WHERE id=$1 AND account_id=$2`, workerID, accountID)
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

func (s *Store) ClaimWorkerController(ctx context.Context, owner string) error {
	if !validWorkerSecret(owner) {
		return errWorkerIdentity
	}
	var claimed string
	err := s.DB.QueryRowContext(ctx, `INSERT INTO direct_worker_controller_lease(singleton,owner,expires_at)
 VALUES(true,$1,now()+interval '60 seconds') ON CONFLICT(singleton) DO UPDATE
 SET owner=EXCLUDED.owner,expires_at=EXCLUDED.expires_at
 WHERE direct_worker_controller_lease.owner=$1 OR direct_worker_controller_lease.expires_at<=now()
 RETURNING owner`, owner).Scan(&claimed)
	if errors.Is(err, sql.ErrNoRows) {
		return errControllerLeaseLost
	}
	return err
}

// DirectWorkerForService distinguishes an opted-in but offline/revoked worker
// from a legacy slot. Only the latter may fall back to Railway transport.
func (s *Store) DirectWorkerForService(ctx context.Context, accountID, providerName, credential, serviceID string) (DirectWorker, bool, error) {
	var w DirectWorker
	var live bool
	err := s.DB.QueryRowContext(ctx, `SELECT w.id::text,w.account_id::text,w.slot_id::text,c.service_id,w.incarnation,w.connection_epoch,w.transport_enabled,
 (w.revoked_at IS NULL AND w.connection_expires_at>now() AND w.replacement_deployment_instance_id IS NULL) IS TRUE
 FROM direct_workers w JOIN compute_slots c ON c.id=w.slot_id AND c.account_id=w.account_id
 WHERE w.account_id=$1 AND c.provider=$2 AND c.provider_credential=$3 AND (c.service_id=$4 OR c.service_name=$4 OR c.deployment_instance_id||'@ssh.railway.com'=$4) AND w.transport_enabled`, accountID, providerName, credential, serviceID).Scan(&w.ID, &w.AccountID, &w.SlotID, &w.ServiceID, &w.Incarnation, &w.Epoch, &w.Enabled, &live)
	if errors.Is(err, sql.ErrNoRows) {
		return DirectWorker{}, false, nil
	}
	if err != nil {
		return DirectWorker{}, false, err
	}
	if !live {
		return w, true, errWorkerOffline
	}
	return w, true, nil
}

func (s *Store) WorkerAssignment(ctx context.Context, w DirectWorker) (fleetAssignment, error) {
	var boxID string
	err := s.DB.QueryRowContext(ctx, `SELECT b.id::text FROM logical_boxes b JOIN compute_slots c ON c.id=b.slot_id AND c.account_id=b.account_id
 WHERE b.account_id=$1 AND b.slot_id=$2 AND b.assignment_generation=c.assignment_generation
 AND b.fencing_token=c.fencing_token AND b.state IN ('reserved','attaching','running','draining','hibernating','deleting')`, w.AccountID, w.SlotID).Scan(&boxID)
	if err != nil {
		return fleetAssignment{}, errors.New("worker has no current box assignment")
	}
	return s.assignment(ctx, w.AccountID, boxID)
}

// WorkerBinding returns either a current logical-box assignment or a free-slot
// maintenance identity. Free identities cannot authorize logical-box endpoints.
func (s *Store) WorkerBinding(ctx context.Context, w DirectWorker) (workerprotocol.Binding, error) {
	binding := workerprotocol.Binding{AccountID: w.AccountID, SlotID: w.SlotID, Incarnation: w.Incarnation}
	assigned, assignmentErr := s.WorkerAssignment(ctx, w)
	if assignmentErr == nil {
		binding.BoxID = assigned.Box.ID
		binding.Assignment = nativeFence(assigned)
		return binding, nil
	}
	// Do not treat an invalid/mismatched box assignment as a free slot. Require
	// the slot to be explicitly free, with no fence and no attached box record.
	var generation int64
	err := s.DB.QueryRowContext(ctx, `SELECT c.assignment_generation FROM compute_slots c
 WHERE c.account_id=$1 AND c.id=$2 AND c.state='free' AND c.fencing_token IS NULL
 AND NOT EXISTS(SELECT 1 FROM logical_boxes b WHERE b.slot_id=c.id)`, w.AccountID, w.SlotID).Scan(&generation)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return workerprotocol.Binding{}, assignmentErr
		}
		return workerprotocol.Binding{}, err
	}
	binding.BoxID = "compute-slot:" + w.SlotID
	binding.Assignment = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("free:%s:%s:%d", w.AccountID, w.SlotID, generation))))
	return binding, nil
}
