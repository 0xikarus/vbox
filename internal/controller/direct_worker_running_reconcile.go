package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

type offlineRunningWorker struct {
	accountID string
	boxID     string
}

// Only an offline, already enrolled running worker needs provider deployment
// evidence. Healthy direct connections never enter this management API path.
func (s *Store) offlineRunningWorkers(ctx context.Context) ([]offlineRunningWorker, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.account_id::text,b.id::text
 FROM logical_boxes b
 JOIN compute_slots c ON c.account_id=b.account_id AND c.id=b.slot_id
 JOIN direct_workers w ON w.account_id=b.account_id AND w.slot_id=c.id
 WHERE b.provider='railway' AND b.state='running' AND c.state='occupied'
 AND b.assignment_generation=c.assignment_generation AND b.fencing_token=c.fencing_token
 AND w.transport_enabled AND w.revoked_at IS NULL
 AND (w.connection_expires_at IS NULL OR w.connection_expires_at<=now())
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
		attemptCtx, cancel := context.WithTimeout(ctx, 75*time.Second)
		a, err := s.Store.assignment(attemptCtx, target.accountID, target.boxID)
		if err == nil && a.Box.State == v1.LogicalBoxRunning && a.Slot.State == v1.FleetSlotOccupied && a.Box.AssignmentGeneration == a.Slot.AssignmentGeneration {
			prov, provErr := s.provider(attemptCtx, target.accountID, a.Box.Provider, a.Box.ProviderCredential)
			if provErr == nil {
				err = s.ensureReplacementWorkerTransport(attemptCtx, target.accountID, a, prov)
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
