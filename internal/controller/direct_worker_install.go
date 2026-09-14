package controller

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/workeragent"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

func (s *Server) installWorkerAgent(w http.ResponseWriter, r *http.Request, p Principal) {
	if !s.DirectWorkersEnabled || len(s.WorkerAgent) == 0 {
		writeError(w, 409, errors.New("worker agent installation is not enabled"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	var boxID string
	err := s.Store.DB.QueryRowContext(ctx, `SELECT id::text FROM logical_boxes WHERE account_id=$1 AND slot_id=$2 AND state='running'`, p.AccountID, r.PathValue("slot")).Scan(&boxID)
	if err != nil {
		writeError(w, 409, errors.New("running box required for sidecar installation"))
		return
	}
	a, err := s.Store.assignment(ctx, p.AccountID, boxID)
	if err != nil || a.Box.Provider != "railway" {
		writeError(w, 409, errWorkerIdentity)
		return
	}
	prov, err := s.provider(ctx, p.AccountID, a.Box.Provider, a.Box.ProviderCredential)
	if err != nil {
		writeError(w, 502, errors.New("worker bootstrap transport unavailable"))
		return
	}
	worker, err := s.installWorkerAgentForAssignment(ctx, p.AccountID, a, prov, nil)
	if err != nil {
		status := 409
		var recoverable workerInstallationError
		if errors.As(err, &recoverable) && recoverable.recoverable {
			status = 502
		}
		writeError(w, status, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 202, map[string]any{"worker": worker, "state": "awaiting-verification"})
}

// installWorkerAgentForAssignment performs the one authorized legacy bootstrap.
// It accepts the exact attaching state used by a new allocation and the running
// state used by explicit migration. Existing enrollment rows are never replaced.
func (s *Server) installWorkerAgentForAssignment(ctx context.Context, accountID string, a fleetAssignment, prov provider.Provider, resolved *provider.Connection) (DirectWorker, error) {
	var empty DirectWorker
	if !s.DirectWorkersEnabled || len(s.WorkerAgent) == 0 {
		return empty, errors.New("worker agent installation is not enabled")
	}
	if a.Box.Provider != "railway" || a.Box.AccountID != accountID || (a.Box.State != "attaching" && a.Box.State != "running") {
		return empty, errWorkerIdentity
	}
	worker, token, err := s.Store.IssueWorkerEnrollment(ctx, accountID, a.Slot.ID)
	if err != nil {
		return empty, errors.New("worker enrollment already exists or is unavailable; inspect its state before retrying")
	}
	attempted := false
	defer func() {
		if !attempted {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_ = s.Store.DiscardUnusedWorkerEnrollment(cleanup, worker, token)
		}
	}()
	// Enrollment issuance rejects existing workers, so this fresh installation
	// can use the underlying legacy transport without a second store lookup while
	// the assignment transaction is held.
	if direct, ok := prov.(*directWorkerProvider); ok {
		prov = direct.Provider
	}
	var connection provider.Connection
	if resolved == nil {
		connection, err = prov.Connection(ctx, a.Slot.ServiceID)
	} else {
		connection = *resolved
	}
	deploymentID := connection.Metadata["deploymentInstanceId"]
	validRailwayTarget := deploymentID != "" && connection.Transport == "openssh" && connection.Endpoint == deploymentID+"@ssh.railway.com"
	if err != nil || !validRailwayTarget || (a.Box.State == "running" && !connectionMatchesAssignment(connection, a)) {
		return empty, errors.New("worker bootstrap deployment could not be fenced")
	}
	executor, ok := prov.(provider.ConnectionExecutor)
	if !ok {
		return empty, errors.New("worker bootstrap transport does not support a fenced connection")
	}
	bootstrapDeploymentID := deploymentID
	binding := workerprotocol.Binding{AccountID: accountID, SlotID: a.Slot.ID, BoxID: a.Box.ID, Assignment: nativeFence(a)}
	payload, err := workeragent.DurableInstallationPayload(workeragent.Config{ControllerURL: s.PublicURL, AccountID: accountID, SlotID: a.Slot.ID, WorkerID: worker.ID, EnrollmentToken: token}, binding, s.WorkerAgent, runtime.GOARCH)
	if err != nil {
		return empty, errors.New("worker installation configuration unavailable")
	}
	// A running box is an explicit migration and must prove that every existing
	// session survives the transport change. An attaching box has not begun any
	// workspace runtime work yet, so its fenced baseline is constructed as empty.
	// This leaves the installation payload as its only legacy runtime command.
	var baselineJSON []byte
	if a.Box.State == "running" {
		baseline, baselineErr := executor.ExecConnection(ctx, connection, []string{"vmbox-runtime", "native-sessions", binding.Assignment}, provider.ExecOptions{})
		if baselineErr != nil || baseline.ExitCode != 0 {
			return empty, errors.New("existing sessions could not be verified before installation")
		}
		if _, err := migrationInventory([]byte(baseline.Stdout), binding.Assignment); err != nil {
			return empty, err
		}
		baselineJSON = []byte(baseline.Stdout)
	} else {
		baselineJSON, err = json.Marshal(v1.SessionInventory{Assignment: binding.Assignment, State: "live", Sessions: []v1.Session{}})
		if err != nil {
			return empty, errors.New("empty session baseline could not be encoded")
		}
	}
	baselineSaved, err := s.Store.DB.ExecContext(ctx, `UPDATE direct_workers SET migration_sessions=$1::jsonb,bootstrap_deployment_id=$4 WHERE id=$2 AND account_id=$3 AND NOT transport_enabled AND migration_sessions IS NULL AND bootstrap_deployment_id IS NULL`, baselineJSON, worker.ID, accountID, bootstrapDeploymentID)
	if err != nil {
		return empty, errors.New("session baseline could not be saved")
	}
	if changed, _ := baselineSaved.RowsAffected(); changed != 1 {
		return empty, errors.New("session baseline changed before installation")
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, errors.New("worker assignment lock unavailable")
	}
	defer tx.Rollback()
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT b.assignment_generation FROM logical_boxes b JOIN compute_slots c ON c.id=b.slot_id AND c.account_id=b.account_id WHERE b.id=$1 AND b.account_id=$2 AND b.state=$5 AND b.slot_id=$3 AND b.fencing_token=$4 AND b.assignment_generation=c.assignment_generation AND b.fencing_token=c.fencing_token FOR UPDATE OF b,c`, a.Box.ID, accountID, a.Slot.ID, a.FencingToken, a.Box.State).Scan(&generation)
	if err != nil || generation != a.Box.AssignmentGeneration {
		return empty, errors.New("worker assignment changed before installation")
	}
	// This is the explicitly selected one-time legacy bootstrap. All secrets and
	// binary bytes travel in stdin; response/error bodies never contain them.
	attempted = true
	result, err := executor.ExecConnection(ctx, connection, []string{"sudo", "-n", "sh", "-s"}, provider.ExecOptions{Stdin: bytes.NewReader(payload)})
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "worker-agent-installed" {
		return worker, workerInstallationError{recoverable: true, err: errors.New("worker installation incomplete; inspect enrollment before retrying")}
	}
	return worker, nil
}

type workerEnrollmentState struct {
	Worker              DirectWorker
	Credential          bool
	Enrollment          bool
	Live                bool
	BootstrapDeployment string
}

type workerInstallationError struct {
	recoverable bool
	err         error
}

func (e workerInstallationError) Error() string { return e.err.Error() }

func (s *Store) workerEnrollmentForSlot(ctx context.Context, accountID, slotID string) (workerEnrollmentState, error) {
	var state workerEnrollmentState
	err := s.DB.QueryRowContext(ctx, `SELECT id::text,account_id::text,slot_id::text,incarnation,connection_epoch,transport_enabled,credential_hash IS NOT NULL,enrollment_hash IS NOT NULL,(revoked_at IS NULL AND connection_expires_at>now()) IS TRUE,COALESCE(bootstrap_deployment_id,'') FROM direct_workers WHERE slot_id=$1 AND account_id=$2 AND revoked_at IS NULL`, slotID, accountID).Scan(&state.Worker.ID, &state.Worker.AccountID, &state.Worker.SlotID, &state.Worker.Incarnation, &state.Worker.Epoch, &state.Worker.Enabled, &state.Credential, &state.Enrollment, &state.Live, &state.BootstrapDeployment)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return state, fmt.Errorf("inspect worker enrollment: %w", err)
	}
	return state, err
}
