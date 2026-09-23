package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type offlineRunningWorker struct {
	accountID string
	boxID     string
}

// A replacement remains pending after its agent reconnects until the retained
// workspace runtime and native sessions have been restored on the new compute.
// The slot deployment is the durable last-completed deployment marker.
func (s *Store) offlineRunningWorkers(ctx context.Context) ([]offlineRunningWorker, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.account_id::text,b.id::text
 FROM logical_boxes b
 JOIN compute_slots c ON c.account_id=b.account_id AND c.id=b.slot_id
 JOIN direct_workers w ON w.account_id=b.account_id AND w.slot_id=c.id
 WHERE b.provider='railway' AND b.state='running' AND c.state='occupied'
 AND b.assignment_generation=c.assignment_generation AND b.fencing_token=c.fencing_token
 AND w.transport_enabled AND w.revoked_at IS NULL
 AND (w.connection_expires_at IS NULL OR w.connection_expires_at<=now()
 OR w.replacement_deployment_instance_id IS NOT NULL
 OR (w.bootstrap_deployment_id IS NOT NULL AND c.deployment_instance_id IS DISTINCT FROM w.bootstrap_deployment_id))
 ORDER BY b.account_id,b.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []offlineRunningWorker
	for rows.Next() {
		var target offlineRunningWorker
		if err := rows.Scan(&target.accountID, &target.boxID); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (s *Store) runningWorkerRuntimeRecoveryDeployment(ctx context.Context, a fleetAssignment) (string, error) {
	var deployment string
	err := s.DB.QueryRowContext(ctx, `SELECT w.bootstrap_deployment_id
 FROM logical_boxes b JOIN compute_slots c ON c.id=b.slot_id AND c.account_id=b.account_id
 JOIN direct_workers w ON w.slot_id=c.id AND w.account_id=c.account_id
 WHERE b.account_id=$1 AND b.id=$2 AND b.slot_id=$3 AND b.state='running' AND c.state='occupied'
 AND b.assignment_generation=$4 AND c.assignment_generation=$4 AND b.fencing_token=$5 AND c.fencing_token=$5
 AND w.transport_enabled AND w.revoked_at IS NULL AND w.connection_expires_at>now()
 AND w.replacement_deployment_instance_id IS NULL AND w.bootstrap_deployment_id IS NOT NULL
 AND c.deployment_instance_id IS DISTINCT FROM w.bootstrap_deployment_id`,
		a.Box.AccountID, a.Box.ID, a.Slot.ID, a.Box.AssignmentGeneration, a.FencingToken).Scan(&deployment)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return deployment, err
}

func (s *Store) completeRunningWorkerRuntimeRecovery(ctx context.Context, a fleetAssignment, deployment string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE compute_slots c SET deployment_instance_id=$1,updated_at=now()
 FROM logical_boxes b,direct_workers w
 WHERE c.id=$2 AND c.account_id=$3 AND c.state='occupied' AND c.assignment_generation=$4 AND c.fencing_token=$5
 AND b.id=$6 AND b.account_id=c.account_id AND b.slot_id=c.id AND b.state='running'
 AND b.assignment_generation=c.assignment_generation AND b.fencing_token=c.fencing_token
 AND w.slot_id=c.id AND w.account_id=c.account_id AND w.transport_enabled AND w.revoked_at IS NULL
 AND w.connection_expires_at>now() AND w.replacement_deployment_instance_id IS NULL
 AND w.bootstrap_deployment_id=$1 AND c.deployment_instance_id IS DISTINCT FROM $1`,
		deployment, a.Slot.ID, a.Box.AccountID, a.Box.AssignmentGeneration, a.FencingToken, a.Box.ID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errWorkerIdentity
	}
	return nil
}

func (s *Server) restoreRunningWorkerRuntime(ctx context.Context, a fleetAssignment, prov provider.Provider) error {
	deployment, err := s.Store.runningWorkerRuntimeRecoveryDeployment(ctx, a)
	if err != nil || deployment == "" {
		return err
	}
	if len(s.WorkerRuntime) == 0 {
		return errors.New("matching workspace runtime unavailable")
	}
	if err := stageWorkspaceRuntime(ctx, prov, a.Slot.ServiceID, s.WorkerRuntime); err != nil {
		return err
	}
	commands := [][]string{
		{"vmbox-runtime", "restore-tools"},
		{"vmbox-runtime", "native-bind", nativeFence(a)},
		{"vmbox-runtime", "tmux-restore"},
		{"vmbox-runtime", "tmux-context", a.Box.Name, a.Slot.ServiceName, "running", "connected"},
		{"vmbox-runtime", "native-sessions", nativeFence(a)},
	}
	for _, argv := range commands {
		result, runErr := prov.Exec(ctx, a.Slot.ServiceID, argv, provider.ExecOptions{})
		if runErr != nil {
			return fmt.Errorf("restore %s: %w", argv[1], runErr)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("restore %s exited with status %d: %s", argv[1], result.ExitCode, strings.TrimSpace(result.Stderr))
		}
	}
	return s.Store.completeRunningWorkerRuntimeRecovery(ctx, a, deployment)
}

// A compute redeploy can replace a running box without creating an allocation.
// Recover its authenticated agent from the new, freshly resolved deployment;
// the existing replacement path fences old authority before installation.
func (s *Server) ReconcileRunningWorkerReplacementsNow(ctx context.Context) error {
	if s.Store == nil {
		return nil
	}
	targets, err := s.Store.offlineRunningWorkers(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 9*time.Minute)
		a, err := s.Store.assignment(attemptCtx, target.accountID, target.boxID)
		if err == nil && a.Box.State == v1.LogicalBoxRunning && a.Slot.State == v1.FleetSlotOccupied && a.Box.AssignmentGeneration == a.Slot.AssignmentGeneration {
			prov, provErr := s.provider(attemptCtx, target.accountID, a.Box.Provider, a.Box.ProviderCredential)
			if provErr == nil {
				err = s.ensureReplacementWorkerTransport(attemptCtx, target.accountID, a, prov)
				if err == nil {
					err = s.restoreRunningWorkerRuntime(attemptCtx, a, prov)
				}
			} else {
				err = provErr
			}
		}
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("running box %s: %w", target.boxID, err))
		}
	}
	return errors.Join(failures...)
}

func (s *Server) startRunningWorkerReplacementReconciler(ctx context.Context) {
	if s.Store == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			if err := s.ReconcileRunningWorkerReplacementsNow(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Warn("running worker replacement reconciliation pending", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
