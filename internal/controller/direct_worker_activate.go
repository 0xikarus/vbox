package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

var errWorkerNotConnected = errors.New("enrolled worker is not connected")

const emptyNativeSessionMarker = "worker-agent-native-sessions-empty"

const verifyEmptyNativeSessions = `set -eu
out="$(mktemp)"
err="$(mktemp)"
trap 'rm -f -- "$out" "$err"' EXIT
if tmux list-sessions -F '#{session_id}' >"$out" 2>"$err"; then
  test ! -s "$out"
else
  socket="${TMUX_TMPDIR:-/tmp}/tmux-$(id -u)/default"
  failure="$(cat "$err")"
  if [ "$failure" = "error connecting to $socket (No such file or directory)" ] && [ ! -e "$socket" ]; then
    :
  elif [ "$failure" = "no server running on $socket" ]; then
    :
  elif [ "$failure" = "no sessions" ]; then
    :
  else
    cat "$err" >&2
    exit 1
  fi
fi
printf '` + emptyNativeSessionMarker + `'`

func migrationInventory(data []byte, fence string) (v1.SessionInventory, error) {
	var inventory v1.SessionInventory
	if len(data) > 1024*1024 || json.Unmarshal(data, &inventory) != nil || inventory.Assignment != fence || inventory.State != "live" || inventory.Partial || len(inventory.Sessions) > 64 {
		return inventory, errors.New("complete live session inventory required")
	}
	seen := map[string]bool{}
	for _, session := range inventory.Sessions {
		if !regexp.MustCompile(`^\$[0-9]+$`).MatchString(session.ID) || session.Name == "" || session.Partial || seen[session.ID] || !regexp.MustCompile(`^`+regexp.QuoteMeta(fence)+`:[a-f0-9]{24}:`+regexp.QuoteMeta(session.ID)+`$`).MatchString(session.Incarnation) {
			return inventory, errors.New("invalid migration session identity")
		}
		seen[session.ID] = true
	}
	return inventory, nil
}

func survivingMigrationSessions(before, after v1.SessionInventory) bool {
	if before.Assignment != after.Assignment {
		return false
	}
	current := map[string]v1.Session{}
	for _, session := range after.Sessions {
		current[session.ID] = session
	}
	for _, session := range before.Sessions {
		now, ok := current[session.ID]
		if !ok || now.Name != session.Name || now.Incarnation != session.Incarnation {
			return false
		}
	}
	return true
}

func (s *Server) activateWorkerAgent(w http.ResponseWriter, r *http.Request, principal Principal) {
	if !s.DirectWorkersEnabled {
		writeError(w, 409, errors.New("direct workers are disabled"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	worker, err := s.activateWorkerForSlot(ctx, principal.AccountID, r.PathValue("slot"))
	if err != nil {
		writeError(w, 409, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"workerId": worker.ID, "transport": directWorkerTransport})
}

// activateWorkerForSlot verifies the authenticated agent against the preserved
// pre-installation session baseline and atomically enables its exact assignment.
// Both new allocations (attaching) and explicit migrations (running) use this.
func (s *Server) activateWorkerForSlot(ctx context.Context, accountID, slotID string) (DirectWorker, error) {
	var worker DirectWorker
	var baseline []byte
	err := s.Store.DB.QueryRowContext(ctx, `SELECT id::text,account_id::text,slot_id::text,incarnation,connection_epoch,transport_enabled,migration_sessions FROM direct_workers WHERE slot_id=$1 AND account_id=$2 AND revoked_at IS NULL AND connection_expires_at>now() AND credential_hash IS NOT NULL`, slotID, accountID).Scan(&worker.ID, &worker.AccountID, &worker.SlotID, &worker.Incarnation, &worker.Epoch, &worker.Enabled, &baseline)
	if err != nil {
		return worker, errWorkerNotConnected
	}
	a, err := s.Store.WorkerAssignment(ctx, worker)
	if err != nil || (a.Box.State != "attaching" && a.Box.State != "running") {
		return worker, errors.New("current attaching or running assignment required")
	}
	before, err := migrationInventory(baseline, nativeFence(a))
	if err != nil {
		return worker, err
	}
	s.mu.Lock()
	connection := s.directWorkers[worker.ID]
	s.mu.Unlock()
	if connection == nil || connection.Worker.Epoch != worker.Epoch || connection.Worker.Incarnation != worker.Incarnation {
		return worker, errWorkerNotConnected
	}
	binding := workerprotocol.Binding{AccountID: worker.AccountID, BoxID: a.Box.ID, SlotID: worker.SlotID, Assignment: nativeFence(a), Incarnation: worker.Incarnation}
	if a.Box.State == "attaching" {
		if len(before.Sessions) != 0 {
			return worker, errors.New("attaching worker requires an empty session baseline")
		}
		if len(s.WorkerRuntime) == 0 {
			return worker, errors.New("matching workspace runtime is unavailable")
		}
		if err := prepareAttachingWorkerRuntime(ctx, s.WorkerRuntime, binding.Assignment, func(ctx context.Context, argv []string, options provider.ExecOptions) (provider.ExecResult, error) {
			return executePendingWorker(ctx, connection, binding, argv, options)
		}); err != nil {
			return worker, errors.New("worker assignment binding failed")
		}
	}
	output, err := executeWorkerVerification(ctx, connection, binding, []string{"vmbox-runtime", "native-sessions", binding.Assignment})
	if err != nil {
		return worker, errors.New("worker session verification failed")
	}
	after, err := migrationInventory(output, binding.Assignment)
	if err != nil || (a.Box.State == "attaching" && len(after.Sessions) != 0) || !survivingMigrationSessions(before, after) {
		return worker, errors.New("existing sessions changed; worker remains on its current transport")
	}
	if err := s.Store.ActivateVerifiedWorker(ctx, worker, a, baseline); err != nil {
		return worker, errors.New("worker assignment or connection changed during verification")
	}
	worker.Enabled = true
	return worker, nil
}

func prepareAttachingWorkerRuntime(ctx context.Context, runtime []byte, assignment string, execute func(context.Context, []string, provider.ExecOptions) (provider.ExecResult, error)) error {
	if err := stageWorkspaceRuntimeWithExec(ctx, runtime, execute); err != nil {
		return err
	}
	return prepareAttachingWorker(ctx, assignment, func(ctx context.Context, argv []string) ([]byte, error) {
		result, err := execute(ctx, argv, provider.ExecOptions{})
		if err != nil || result.ExitCode != 0 {
			return nil, errors.New("worker preparation command failed")
		}
		return []byte(result.Stdout), nil
	})
}

func prepareAttachingWorker(ctx context.Context, assignment string, run func(context.Context, []string) ([]byte, error)) error {
	output, err := run(ctx, []string{"sh", "-c", verifyEmptyNativeSessions})
	if err != nil || strings.TrimSpace(string(output)) != emptyNativeSessionMarker {
		return errors.New("empty native session inventory could not be proven")
	}
	_, err = run(ctx, []string{"vmbox-runtime", "native-bind", assignment})
	return err
}

func executeWorkerVerification(ctx context.Context, connection *directWorkerConnection, binding workerprotocol.Binding, argv []string) ([]byte, error) {
	result, err := executePendingWorker(ctx, connection, binding, argv, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		return nil, errors.New("worker verification command failed")
	}
	return []byte(result.Stdout), nil
}

func executePendingWorker(ctx context.Context, connection *directWorkerConnection, binding workerprotocol.Binding, argv []string, options provider.ExecOptions) (provider.ExecResult, error) {
	result := provider.ExecResult{StartedAt: time.Now().UTC()}
	stream, err := connection.Peer.Open(ctx, workerprotocol.Request{Binding: binding, OperationID: uuid(), Argv: provider.AsWorkloadUser(argv)})
	if err != nil {
		return result, err
	}
	defer stream.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			stream.Close()
		case <-stream.Done():
		}
	}()
	writeDone := make(chan error, 1)
	if options.Stdin == nil {
		writeDone <- stream.CloseWrite()
	} else {
		go func() {
			_, copyErr := io.Copy(stream, options.Stdin)
			closeErr := stream.CloseWrite()
			if copyErr != nil {
				writeDone <- copyErr
			} else {
				writeDone <- closeErr
			}
		}()
	}
	var stdout, stderr workerCapture
	result.ExitCode, err = workerprotocol.ReadOutput(stream, &stdout, &stderr)
	if err != nil {
		cancel()
	}
	// An exit frame ends the operation. Close before joining the upload writer
	// so a peer that replied without consuming stdin cannot strand activation.
	_ = stream.Close()
	result.FinishedAt = time.Now().UTC()
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	if writeErr := <-writeDone; writeErr != nil && err == nil {
		err = writeErr
	}
	return result, err
}

func (s *Store) ActivateVerifiedWorker(ctx context.Context, worker DirectWorker, a fleetAssignment, baseline []byte) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT b.assignment_generation FROM logical_boxes b JOIN compute_slots c ON c.id=b.slot_id AND c.account_id=b.account_id WHERE b.id=$1 AND b.account_id=$2 AND b.slot_id=$3 AND b.state=$5 AND b.fencing_token=$4 AND c.fencing_token=b.fencing_token AND c.assignment_generation=b.assignment_generation FOR UPDATE OF b,c`, a.Box.ID, worker.AccountID, worker.SlotID, a.FencingToken, a.Box.State).Scan(&generation)
	if err != nil || generation != a.Box.AssignmentGeneration {
		return errWorkerIdentity
	}
	result, err := tx.ExecContext(ctx, `UPDATE direct_workers SET transport_enabled=true WHERE id=$1 AND account_id=$2 AND slot_id=$3 AND connection_epoch=$4 AND incarnation=$5 AND revoked_at IS NULL AND connection_expires_at>now() AND migration_sessions=$6::jsonb AND bootstrap_deployment_id IS NOT NULL AND NOT transport_enabled`, worker.ID, worker.AccountID, worker.SlotID, worker.Epoch, worker.Incarnation, baseline)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return errWorkerIdentity
	}
	return tx.Commit()
}
