package controller

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	"github.com/0xikarus/vmbox-service/internal/workeragent"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

func (s *Server) recoverWorkerAgent(w http.ResponseWriter, r *http.Request, p Principal) {
	if !s.DirectWorkersEnabled {
		writeError(w, 409, errors.New("worker recovery is not enabled"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	var boxID string
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT b.id::text FROM logical_boxes b JOIN compute_slots c ON c.id=b.slot_id AND c.account_id=b.account_id WHERE b.account_id=$1 AND b.slot_id=$2 AND b.state IN ('attaching','running') AND b.assignment_generation=c.assignment_generation AND b.fencing_token=c.fencing_token`, p.AccountID, r.PathValue("slot")).Scan(&boxID); err != nil {
		writeError(w, 409, errors.New("current attaching or running assignment required"))
		return
	}
	a, err := s.Store.assignment(ctx, p.AccountID, boxID)
	if err != nil {
		writeError(w, 409, errWorkerIdentity)
		return
	}
	prov, err := s.provider(ctx, p.AccountID, a.Box.Provider, a.Box.ProviderCredential)
	if err != nil {
		writeError(w, 502, errors.New("worker recovery transport unavailable"))
		return
	}
	worker, err := s.recoverWorkerForAssignment(ctx, p.AccountID, a, prov)
	if err != nil {
		status := 502
		var conflict workerRecoveryConflict
		if errors.As(err, &conflict) || errors.Is(err, errWorkerIdentity) {
			status = 409
		}
		writeError(w, status, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 202, map[string]any{"worker": worker, "state": "awaiting-verification"})
}

type pendingWorkerRecovery struct {
	Worker              DirectWorker
	ServiceID           string
	Baseline            []byte
	BootstrapDeployment string
	Enrollment          bool
	Credential          bool
}

type workerRecoveryConflict struct{ err error }

func (e workerRecoveryConflict) Error() string { return e.err.Error() }
func (e workerRecoveryConflict) Unwrap() error { return e.err }

func recoveryConflict(message string) error {
	return workerRecoveryConflict{err: errors.New(message)}
}

// recoverWorkerForAssignment probes one pinned bootstrap deployment. Existing
// configuration is identity-verified and restarted. A token is rotated and the
// durable installation submitted once only after that target explicitly reports
// the configuration absent; transport errors preserve the prior identity.
func (s *Server) recoverWorkerForAssignment(ctx context.Context, accountID string, a fleetAssignment, prov provider.Provider) (DirectWorker, error) {
	var pending pendingWorkerRecovery
	err := s.Store.DB.QueryRowContext(ctx, `SELECT w.id::text,w.account_id::text,w.slot_id::text,c.service_id,w.incarnation,w.connection_epoch,w.transport_enabled,w.migration_sessions,COALESCE(w.bootstrap_deployment_id,''),w.enrollment_hash IS NOT NULL,w.credential_hash IS NOT NULL
 FROM direct_workers w JOIN compute_slots c ON c.id=w.slot_id AND c.account_id=w.account_id
 WHERE w.account_id=$1 AND w.slot_id=$2 AND w.revoked_at IS NULL AND NOT w.transport_enabled`, accountID, a.Slot.ID).Scan(&pending.Worker.ID, &pending.Worker.AccountID, &pending.Worker.SlotID, &pending.ServiceID, &pending.Worker.Incarnation, &pending.Worker.Epoch, &pending.Worker.Enabled, &pending.Baseline, &pending.BootstrapDeployment, &pending.Enrollment, &pending.Credential)
	if err != nil || pending.BootstrapDeployment == "" || (!pending.Enrollment && !pending.Credential) {
		return pending.Worker, recoveryConflict("pending configured worker required for recovery")
	}
	if a.Box.AccountID != accountID || (a.Box.State != "attaching" && a.Box.State != "running") || a.Slot.ID != pending.Worker.SlotID {
		return pending.Worker, workerRecoveryConflict{err: errWorkerIdentity}
	}
	if _, err = migrationInventory(pending.Baseline, nativeFence(a)); err != nil {
		return pending.Worker, recoveryConflict("original session baseline required for recovery")
	}
	backing := prov
	if direct, ok := prov.(*directWorkerProvider); ok {
		backing = direct.Provider
	}
	executor, ok := backing.(provider.ConnectionExecutor)
	if !ok {
		return pending.Worker, errors.New("worker recovery connection executor unavailable")
	}
	connection, err := backing.Connection(ctx, pending.ServiceID)
	deploymentID := connection.Metadata["deploymentInstanceId"]
	if err != nil || deploymentID != pending.BootstrapDeployment || connection.Transport != "openssh" || connection.Endpoint != deploymentID+"@ssh.railway.com" {
		return pending.Worker, recoveryConflict("worker recovery bootstrap deployment changed")
	}
	identity := workeragent.InstallationIdentity{ControllerURL: s.PublicURL, AccountID: accountID, SlotID: pending.Worker.SlotID, WorkerID: pending.Worker.ID}
	recovery, err := workeragent.InstallationRecoveryPayload(identity)
	if err != nil {
		return pending.Worker, errors.New("worker recovery configuration unavailable")
	}
	if pending.Enrollment && !pending.Credential {
		result, renewErr := s.Store.DB.ExecContext(ctx, `UPDATE direct_workers SET enrollment_expires_at=now()+interval '15 minutes' WHERE id=$1 AND account_id=$2 AND slot_id=$3 AND connection_epoch=$4 AND credential_hash IS NULL AND enrollment_hash IS NOT NULL AND revoked_at IS NULL AND NOT transport_enabled`, pending.Worker.ID, accountID, pending.Worker.SlotID, pending.Worker.Epoch)
		if renewErr != nil {
			return pending.Worker, errors.New("worker enrollment recovery unavailable")
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return pending.Worker, errWorkerIdentity
		}
	}
	recoveryLock, err := s.Store.lockPendingWorkerAssignment(ctx, accountID, a)
	if err != nil {
		return pending.Worker, err
	}
	result, runErr := executor.ExecConnection(ctx, connection, []string{"sudo", "-n", "sh", "-s"}, provider.ExecOptions{Stdin: bytes.NewReader(recovery)})
	_ = recoveryLock.Rollback()
	if runErr != nil {
		return pending.Worker, errors.New("worker recovery outcome is ambiguous; existing identity was preserved")
	}
	output := strings.TrimSpace(result.Stdout)
	if result.ExitCode == 0 && output == "worker-agent-installation-recovery-started" {
		return pending.Worker, nil
	}
	if result.ExitCode != 66 || output != "worker-agent-installation-absent" {
		return pending.Worker, recoveryConflict("worker recovery was rejected; existing identity was preserved")
	}
	if pending.Credential {
		return pending.Worker, recoveryConflict("credentialed worker configuration is absent; automatic reissue refused")
	}
	token, err := s.Store.resetAbsentWorkerEnrollment(ctx, accountID, a, pending, deploymentID)
	if err != nil {
		return pending.Worker, err
	}
	binding := workerprotocol.Binding{AccountID: accountID, SlotID: a.Slot.ID, BoxID: a.Box.ID, Assignment: nativeFence(a)}
	install, err := workeragent.DurableInstallationPayload(workeragent.Config{ControllerURL: s.PublicURL, AccountID: accountID, SlotID: a.Slot.ID, WorkerID: pending.Worker.ID, EnrollmentToken: token}, binding, s.WorkerAgent, runtime.GOARCH)
	if err != nil {
		return pending.Worker, errors.New("worker reinstallation configuration unavailable")
	}
	installLock, err := s.Store.lockPendingWorkerAssignment(ctx, accountID, a)
	if err != nil {
		return pending.Worker, err
	}
	installed, installErr := executor.ExecConnection(ctx, connection, []string{"sudo", "-n", "sh", "-s"}, provider.ExecOptions{Stdin: bytes.NewReader(install)})
	_ = installLock.Rollback()
	if installErr != nil {
		return pending.Worker, errors.New("worker reinstallation outcome is ambiguous; rotated identity was preserved")
	}
	if installed.ExitCode != 0 || strings.TrimSpace(installed.Stdout) != "worker-agent-installed" {
		return pending.Worker, errors.New("worker reinstallation was rejected; rotated identity was preserved")
	}
	return pending.Worker, nil
}

func (s *Store) lockPendingWorkerAssignment(ctx context.Context, accountID string, a fleetAssignment) (*sql.Tx, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT b.assignment_generation FROM logical_boxes b JOIN compute_slots c ON c.id=b.slot_id AND c.account_id=b.account_id WHERE b.id=$1 AND b.account_id=$2 AND b.slot_id=$3 AND b.state=$6 AND b.assignment_generation=$4 AND b.fencing_token=$5 AND c.assignment_generation=b.assignment_generation AND c.fencing_token=b.fencing_token FOR UPDATE OF b,c`, a.Box.ID, accountID, a.Slot.ID, a.Box.AssignmentGeneration, a.FencingToken, a.Box.State).Scan(&generation)
	if err != nil || generation != a.Box.AssignmentGeneration {
		_ = tx.Rollback()
		return nil, errWorkerIdentity
	}
	return tx, nil
}

func (s *Store) resetAbsentWorkerEnrollment(ctx context.Context, accountID string, a fleetAssignment, pending pendingWorkerRecovery, deploymentID string) (string, error) {
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
	err = tx.QueryRowContext(ctx, `SELECT b.assignment_generation FROM logical_boxes b JOIN compute_slots c ON c.id=b.slot_id AND c.account_id=b.account_id WHERE b.id=$1 AND b.account_id=$2 AND b.slot_id=$3 AND b.state=$6 AND b.assignment_generation=$4 AND b.fencing_token=$5 AND c.assignment_generation=b.assignment_generation AND c.fencing_token=b.fencing_token FOR UPDATE OF b,c`, a.Box.ID, accountID, a.Slot.ID, a.Box.AssignmentGeneration, a.FencingToken, a.Box.State).Scan(&generation)
	if err != nil || generation != a.Box.AssignmentGeneration {
		return "", errWorkerIdentity
	}
	result, err := tx.ExecContext(ctx, `UPDATE direct_workers SET enrollment_hash=$1,enrollment_expires_at=now()+interval '15 minutes',connection_epoch=connection_epoch+1,incarnation='',connection_owner='',connection_expires_at=NULL,observation='{}'::jsonb,observed_at=NULL WHERE id=$2 AND account_id=$3 AND slot_id=$4 AND connection_epoch=$5 AND credential_hash IS NULL AND enrollment_hash IS NOT NULL AND revoked_at IS NULL AND NOT transport_enabled AND migration_sessions=$6::jsonb AND bootstrap_deployment_id=$7`, secrets.TokenHash(token), pending.Worker.ID, accountID, a.Slot.ID, pending.Worker.Epoch, pending.Baseline, deploymentID)
	if err != nil {
		return "", err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return "", errWorkerIdentity
	}
	return token, tx.Commit()
}
