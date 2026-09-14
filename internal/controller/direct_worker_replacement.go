package controller

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"runtime"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	"github.com/0xikarus/vmbox-service/internal/workeragent"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

type workerReplacement struct {
	Worker     DirectWorker
	Installed  string
	Target     string
	Enrollment bool
	Credential bool
	Live       bool
}

// beginWorkerReplacement rotates authority only after the caller supplies a
// freshly resolved deployment instance that differs from the verified install.
// transport_enabled deliberately remains true, so ordinary operations see an
// enrolled worker reconnecting and can never fall back to Railway SSH.
func (s *Store) beginWorkerReplacement(ctx context.Context, accountID string, a fleetAssignment, deploymentID string) (workerReplacement, string, bool, error) {
	var state workerReplacement
	if deploymentID == "" || a.Box.AccountID != accountID || a.Slot.ID == "" || a.FencingToken == "" {
		return state, "", false, errWorkerIdentity
	}
	token, err := secretToken()
	if err != nil {
		return state, "", false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return state, "", false, err
	}
	defer tx.Rollback()
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT b.assignment_generation FROM logical_boxes b JOIN compute_slots c ON c.id=b.slot_id AND c.account_id=b.account_id
 WHERE b.account_id=$1 AND b.id=$2 AND b.slot_id=$3 AND b.assignment_generation=$4 AND b.fencing_token=$5
 AND c.assignment_generation=b.assignment_generation AND c.fencing_token=b.fencing_token
 AND b.state IN ('attaching','running') FOR UPDATE OF b,c`, accountID, a.Box.ID, a.Slot.ID, a.Box.AssignmentGeneration, a.FencingToken).Scan(&generation)
	if err != nil || generation != a.Box.AssignmentGeneration {
		return state, "", false, errWorkerIdentity
	}
	err = tx.QueryRowContext(ctx, `SELECT id::text,account_id::text,slot_id::text,incarnation,connection_epoch,transport_enabled,
 COALESCE(bootstrap_deployment_id,''),COALESCE(replacement_deployment_instance_id,''),
 enrollment_hash IS NOT NULL,credential_hash IS NOT NULL,(connection_expires_at>now()) IS TRUE
 FROM direct_workers WHERE account_id=$1 AND slot_id=$2 AND revoked_at IS NULL AND transport_enabled FOR UPDATE`, accountID, a.Slot.ID).Scan(
		&state.Worker.ID, &state.Worker.AccountID, &state.Worker.SlotID, &state.Worker.Incarnation, &state.Worker.Epoch, &state.Worker.Enabled,
		&state.Installed, &state.Target, &state.Enrollment, &state.Credential, &state.Live)
	if errors.Is(err, sql.ErrNoRows) {
		return state, "", false, nil
	}
	if err != nil {
		return state, "", false, err
	}
	if state.Installed == "" {
		return state, "", false, errors.New("worker replacement requires a verified bootstrap deployment")
	}
	if state.Installed == deploymentID && state.Target == "" {
		return state, "", false, tx.Commit()
	}
	if state.Target == deploymentID && (state.Enrollment || state.Credential) {
		return state, "", false, tx.Commit()
	}
	if state.Target != "" || !state.Credential || state.Enrollment {
		return state, "", false, errors.New("worker replacement state requires reconciliation")
	}
	oldEpoch, oldIncarnation := state.Worker.Epoch, state.Worker.Incarnation
	result, err := tx.ExecContext(ctx, `UPDATE direct_workers SET enrollment_hash=$1,enrollment_expires_at=now()+interval '15 minutes',
 credential_hash=NULL,enrolled_at=NULL,incarnation='',connection_owner='',connection_epoch=connection_epoch+1,
 connection_expires_at=NULL,observation='{}'::jsonb,observed_at=NULL,replacement_deployment_instance_id=$2,replacement_started_at=now()
 WHERE id=$3 AND account_id=$4 AND slot_id=$5 AND connection_epoch=$6 AND incarnation=$7
 AND bootstrap_deployment_id IS NOT NULL AND bootstrap_deployment_id<>$2
 AND credential_hash IS NOT NULL AND enrollment_hash IS NULL AND revoked_at IS NULL AND transport_enabled`,
		secrets.TokenHash(token), deploymentID, state.Worker.ID, accountID, a.Slot.ID, oldEpoch, oldIncarnation)
	if err != nil {
		return state, "", false, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return state, "", false, errWorkerIdentity
	}
	state.Target, state.Enrollment, state.Credential, state.Live = deploymentID, true, false, false
	state.Worker.Epoch++
	state.Worker.Incarnation = ""
	return state, token, true, tx.Commit()
}

func (s *Server) closeWorkerConnection(workerID string) {
	s.mu.Lock()
	connection := s.directWorkers[workerID]
	s.mu.Unlock()
	if connection != nil {
		connection.Peer.Close()
	}
}

func replacementDeploymentTarget(connection provider.Connection) (string, bool) {
	deploymentID := connection.Metadata["deploymentInstanceId"]
	if deploymentID == "" || connection.Transport != "openssh" || connection.Endpoint != deploymentID+"@ssh.railway.com" {
		return "", false
	}
	for _, character := range deploymentID {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return "", false
	}
	return deploymentID, true
}

// restartWorkerReplacementEnrollment is permitted only after the pinned target
// returned the exact "configuration absent" result. A transport error never
// reaches this method.
func (s *Store) restartWorkerReplacementEnrollment(ctx context.Context, accountID string, a fleetAssignment, state workerReplacement, deploymentID string) (string, error) {
	token, err := secretToken()
	if err != nil {
		return "", err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT b.assignment_generation FROM logical_boxes b JOIN compute_slots c ON c.id=b.slot_id AND c.account_id=b.account_id
 WHERE b.account_id=$1 AND b.id=$2 AND b.slot_id=$3 AND b.assignment_generation=$4 AND b.fencing_token=$5
 AND c.assignment_generation=b.assignment_generation AND c.fencing_token=b.fencing_token
 AND b.state IN ('attaching','running') FOR UPDATE OF b,c`, accountID, a.Box.ID, a.Slot.ID, a.Box.AssignmentGeneration, a.FencingToken).Scan(&generation)
	if err != nil || generation != a.Box.AssignmentGeneration {
		return "", errWorkerIdentity
	}
	result, err := tx.ExecContext(ctx, `UPDATE direct_workers SET enrollment_hash=$1,enrollment_expires_at=now()+interval '15 minutes',
 connection_epoch=connection_epoch+1,connection_owner='',connection_expires_at=NULL,incarnation='',replacement_started_at=now()
 WHERE id=$2 AND account_id=$3 AND slot_id=$4 AND connection_epoch=$5 AND credential_hash IS NULL
 AND enrollment_hash IS NOT NULL AND replacement_deployment_instance_id=$6 AND revoked_at IS NULL AND transport_enabled`,
		secrets.TokenHash(token), state.Worker.ID, accountID, a.Slot.ID, state.Worker.Epoch, deploymentID)
	if err != nil {
		return "", err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return "", errWorkerIdentity
	}
	return token, tx.Commit()
}

// ensureReplacementWorkerTransport is called only from explicit infrastructure
// lifecycle, after Railway has materialized the target deployment and before any
// normal runtime operation. Merely observing an offline WebSocket never reaches
// this helper and never authorizes credential rotation.
func (s *Server) ensureReplacementWorkerTransport(ctx context.Context, accountID string, a fleetAssignment, prov provider.Provider) error {
	if a.Box.Provider != "railway" {
		return nil
	}
	var enabled bool
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM direct_workers WHERE account_id=$1 AND slot_id=$2 AND transport_enabled AND revoked_at IS NULL)`, accountID, a.Slot.ID).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	if err := workeragent.ValidateReplacementInstallation(s.PublicURL, s.WorkerAgent, runtime.GOARCH); err != nil {
		return errors.New("worker replacement configuration unavailable")
	}
	backing := prov
	if direct, ok := prov.(*directWorkerProvider); ok {
		backing = direct.Provider
	}
	executor, ok := backing.(provider.ConnectionExecutor)
	if !ok {
		return errors.New("worker replacement connection executor unavailable")
	}
	connection, err := backing.Connection(ctx, a.Slot.ServiceID)
	if err != nil {
		return errors.New("worker replacement deployment evidence unavailable")
	}
	deploymentID, validTarget := replacementDeploymentTarget(connection)
	if !validTarget {
		return errors.New("worker replacement deployment evidence unavailable")
	}
	state, token, started, err := s.Store.beginWorkerReplacement(ctx, accountID, a, deploymentID)
	if err != nil || state.Worker.ID == "" {
		return err
	}
	if state.Installed == deploymentID && state.Target == "" {
		if !state.Live {
			return errors.New("worker agent offline on unchanged deployment; reconnecting")
		}
		return nil
	}
	install := started
	if !started && state.Target == deploymentID && state.Enrollment && !state.Credential {
		recovery, recoveryErr := workeragent.ReplacementRecoveryPayload(workeragent.InstallationIdentity{ControllerURL: s.PublicURL, AccountID: accountID, SlotID: a.Slot.ID, WorkerID: state.Worker.ID})
		if recoveryErr != nil {
			return errors.New("worker replacement recovery configuration unavailable")
		}
		result, runErr := executor.ExecConnection(ctx, connection, []string{"sudo", "-n", "sh", "-s"}, provider.ExecOptions{Stdin: bytes.NewReader(recovery)})
		if runErr == nil && result.ExitCode == 66 && strings.TrimSpace(result.Stdout) == "worker-agent-installation-absent" {
			var rotateErr error
			token, rotateErr = s.Store.restartWorkerReplacementEnrollment(ctx, accountID, a, state, deploymentID)
			if rotateErr != nil {
				return rotateErr
			}
			install = true
		} else if runErr == nil && (result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "worker-agent-installation-recovery-started") {
			return errors.New("worker replacement recovery was rejected")
		}
	}
	if install {
		s.closeWorkerConnection(state.Worker.ID)
		binding := workerprotocol.Binding{AccountID: accountID, SlotID: a.Slot.ID, BoxID: a.Box.ID, Assignment: nativeFence(a)}
		payload, payloadErr := workeragent.ReplacementInstallationPayload(workeragent.Config{ControllerURL: s.PublicURL, AccountID: accountID, SlotID: a.Slot.ID, WorkerID: state.Worker.ID, EnrollmentToken: token}, binding, s.WorkerAgent, runtime.GOARCH)
		if payloadErr != nil {
			return errors.New("worker replacement configuration unavailable")
		}
		result, installErr := executor.ExecConnection(ctx, connection, []string{"sudo", "-n", "sh", "-s"}, provider.ExecOptions{Stdin: bytes.NewReader(payload)})
		if installErr == nil && (result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "worker-agent-replacement-installed") {
			return errors.New("worker replacement installation was rejected")
		}
		// A transport error is ambiguous. Never submit the installation again in
		// this attempt; the authenticated connection below is the reconciliation.
	}
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.waitForReplacementWorker(waitCtx, accountID, a, state.Worker.ID, deploymentID)
}

func (s *Server) waitForReplacementWorker(ctx context.Context, accountID string, a fleetAssignment, workerID, deploymentID string) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var worker DirectWorker
		var target string
		err := s.Store.DB.QueryRowContext(ctx, `SELECT id::text,account_id::text,slot_id::text,incarnation,connection_epoch,transport_enabled,
 COALESCE(replacement_deployment_instance_id,'') FROM direct_workers
 WHERE id=$1 AND account_id=$2 AND slot_id=$3 AND credential_hash IS NOT NULL AND revoked_at IS NULL
 AND connection_expires_at>now()`, workerID, accountID, a.Slot.ID).Scan(&worker.ID, &worker.AccountID, &worker.SlotID, &worker.Incarnation, &worker.Epoch, &worker.Enabled, &target)
		if err == nil && target == deploymentID {
			s.mu.Lock()
			active := s.directWorkers[worker.ID]
			s.mu.Unlock()
			binding := workerprotocol.Binding{AccountID: accountID, SlotID: a.Slot.ID, BoxID: a.Box.ID, Assignment: nativeFence(a), Incarnation: worker.Incarnation}
			var activeBinding workerprotocol.Binding
			var bindingControl bool
			if active != nil {
				active.assignmentMu.Lock()
				activeBinding, bindingControl = active.binding, active.bindingControl
				active.assignmentMu.Unlock()
			}
			if active != nil && active.Worker.Epoch == worker.Epoch && active.Worker.Incarnation == worker.Incarnation && bindingControl && activeBinding == binding {
				return s.Store.finishWorkerReplacement(ctx, worker, a, deploymentID)
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("worker replacement remains pending; installation was not replayed")
		case <-ticker.C:
		}
	}
}

func (s *Store) finishWorkerReplacement(ctx context.Context, worker DirectWorker, a fleetAssignment, deploymentID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT b.assignment_generation FROM logical_boxes b JOIN compute_slots c ON c.id=b.slot_id AND c.account_id=b.account_id
 WHERE b.id=$1 AND b.account_id=$2 AND b.slot_id=$3 AND b.assignment_generation=$4 AND b.fencing_token=$5
 AND c.assignment_generation=b.assignment_generation AND c.fencing_token=b.fencing_token
 AND b.state IN ('attaching','running') FOR UPDATE OF b,c`, a.Box.ID, worker.AccountID, worker.SlotID, a.Box.AssignmentGeneration, a.FencingToken).Scan(&generation)
	if err != nil || generation != a.Box.AssignmentGeneration {
		return errWorkerIdentity
	}
	result, err := tx.ExecContext(ctx, `UPDATE direct_workers SET bootstrap_deployment_id=$1,replacement_deployment_instance_id=NULL,replacement_started_at=NULL
 WHERE id=$2 AND account_id=$3 AND slot_id=$4 AND connection_epoch=$5 AND incarnation=$6 AND credential_hash IS NOT NULL
 AND revoked_at IS NULL AND connection_expires_at>now() AND replacement_deployment_instance_id=$1`, deploymentID, worker.ID, worker.AccountID, worker.SlotID, worker.Epoch, worker.Incarnation)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errWorkerIdentity
	}
	return tx.Commit()
}
