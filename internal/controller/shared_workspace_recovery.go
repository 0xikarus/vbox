package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Server) recoverSharedWorkspace(ctx context.Context, assignment fleetAssignment, prov provider.Provider) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	connection, err := prov.Connection(ctx, assignment.Slot.ServiceID)
	if err != nil {
		return err
	}
	deployment := connection.Metadata["deploymentInstanceId"]
	expected := assignment
	expected.Slot.DeploymentInstanceID = deployment
	if !connectionMatchesAssignment(connection, expected) {
		return fmt.Errorf("shared workspace identity changed; recovery refused")
	}
	if deployment == assignment.Slot.DeploymentInstanceID {
		return nil
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT assignment_generation FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state='running' AND slot_id=$3 AND fencing_token=$4 FOR UPDATE`, assignment.Box.AccountID, assignment.Box.ID, assignment.Slot.ID, assignment.FencingToken).Scan(&generation)
	if err != nil || generation != assignment.Box.AssignmentGeneration {
		return fmt.Errorf("shared workspace assignment changed; recovery refused")
	}
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(deployment_instance_id,'') FROM compute_slots WHERE account_id=$1 AND id=$2 AND state='occupied' AND assignment_generation=$3 AND fencing_token=$4 FOR UPDATE`, assignment.Box.AccountID, assignment.Slot.ID, generation, assignment.FencingToken).Scan(&current); err != nil {
		return err
	}
	if current == deployment {
		return nil
	}
	if _, err := prov.Start(ctx, assignment.Slot.ServiceID); err != nil {
		return fmt.Errorf("recover shared workspace account: %w", err)
	}
	if err := stageWorkspaceRuntime(ctx, prov, assignment.Slot.ServiceID, s.WorkerRuntime); err != nil {
		return err
	}
	for _, command := range [][]string{
		{"vmbox-runtime", "tmux-restore"},
		{"vmbox-runtime", "native-bind", nativeFence(assignment)},
		{"vmbox-runtime", "native-sessions", nativeFence(assignment)},
	} {
		result, err := prov.Exec(ctx, assignment.Slot.ServiceID, command, provider.ExecOptions{})
		if err != nil || result.ExitCode != 0 {
			return fmt.Errorf("shared workspace recovery failed at %s", command[1])
		}
	}
	latest, err := prov.Connection(ctx, assignment.Slot.ServiceID)
	if err != nil || !connectionMatchesAssignment(latest, expected) {
		return fmt.Errorf("shared worker changed during recovery")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE compute_slots SET deployment_instance_id=$3,updated_at=now() WHERE account_id=$1 AND id=$2`, assignment.Box.AccountID, assignment.Slot.ID, deployment); err != nil {
		return err
	}
	return tx.Commit()
}
