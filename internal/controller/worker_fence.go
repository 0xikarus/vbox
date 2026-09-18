package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

// workerFenceLost reports the worker-side failure that means the tmux server no
// longer carries this box's assignment. The fence lives in the tmux server's
// global options, so anything that takes that server with it — a worker restart,
// a replaced deployment, a tmux server that exited — leaves the box running with
// a valid database assignment that the worker itself no longer recognizes.
func workerFenceLost(detail string) bool {
	lowered := strings.ToLower(detail)
	for _, marker := range []string{
		"worker assignment unavailable",
		"server incarnation unavailable",
		"desktop startup requires a worker assignment",
	} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// rebindWorkerAssignment re-establishes the fence on the worker. The controller
// is the authority for an assignment, so re-binding one it already granted is a
// repair rather than a new grant — but only for the box the database still shows
// holding that slot at that generation, which is re-checked under lock here. A
// box that lost the slot is refused, so this can never hand a worker to a box
// that no longer owns it.
func (s *Server) rebindWorkerAssignment(ctx context.Context, accountID string, a fleetAssignment, prov provider.Provider) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT assignment_generation FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state='running' AND slot_id=$3 AND fencing_token=$4 FOR UPDATE`,
		accountID, a.Box.ID, a.Slot.ID, a.FencingToken).Scan(&generation)
	if err != nil || generation != a.Box.AssignmentGeneration {
		return fmt.Errorf("assignment changed; worker fence repair refused")
	}
	// A restarted worker also loses the staged runtime binary, so the command
	// below would not exist without this.
	if len(s.WorkerRuntime) > 0 {
		if err := stageWorkspaceRuntime(ctx, prov, a.Slot.ServiceID, s.WorkerRuntime); err != nil {
			return fmt.Errorf("worker runtime staging failed: %w", err)
		}
	}
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "native-bind", nativeFence(a)}, provider.ExecOptions{})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		detail := desktopFailureDetail(result.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("exit status %d", result.ExitCode)
		}
		return fmt.Errorf("worker fence repair failed: %s", detail)
	}
	return tx.Commit()
}
