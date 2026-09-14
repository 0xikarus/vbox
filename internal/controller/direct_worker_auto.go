package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

// ensureAutomaticWorkerTransport makes direct runtime transport part of Railway
// allocation readiness. It creates at most one enrollment. A preserved pending
// enrollment is only observed, so an ambiguous bootstrap can never be replayed.
func (s *Server) ensureAutomaticWorkerTransport(ctx context.Context, accountID string, a fleetAssignment, prov provider.Provider, connection provider.Connection) error {
	if !s.DirectWorkersEnabled || a.Box.Provider != "railway" {
		return nil
	}
	state, err := s.Store.workerEnrollmentForSlot(ctx, accountID, a.Slot.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var installErr error
	if errors.Is(err, sql.ErrNoRows) {
		_, installErr = s.installWorkerAgentForAssignment(ctx, accountID, a, prov, &connection)
		var classified workerInstallationError
		if installErr != nil && (!errors.As(installErr, &classified) || !classified.recoverable) {
			return installErr
		}
	} else if state.Worker.Enabled {
		if state.BootstrapDeployment == "" {
			return errors.New("enabled worker has no verified bootstrap deployment")
		}
		if !state.Live {
			return errors.New("enabled worker agent is offline; allocation remains pending")
		}
		current, assignmentErr := s.Store.WorkerAssignment(ctx, state.Worker)
		if assignmentErr != nil || !sameWorkerAssignment(current, a) {
			return errors.New("enabled worker belongs to another assignment")
		}
		connection, connectionErr := prov.Connection(ctx, a.Slot.ServiceID)
		if connectionErr != nil || connection.Transport != directWorkerTransport || !connectionMatchesAssignment(connection, a) {
			return errors.New("enabled worker connection is unavailable or changed")
		}
		return nil
	} else if !state.Enrollment && !state.Credential {
		return errors.New("worker enrollment state is ambiguous; inspect before recovery")
	} else if !state.Live {
		if _, recoveryErr := s.recoverWorkerForAssignment(ctx, accountID, a, prov); recoveryErr != nil {
			return recoveryErr
		}
	}

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err = s.activateWorkerForSlot(waitCtx, accountID, a.Slot.ID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errWorkerNotConnected) {
			return err
		}
		select {
		case <-waitCtx.Done():
			if installErr != nil {
				return installErr
			}
			return fmt.Errorf("worker enrollment is preserved but the agent did not connect: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func sameWorkerAssignment(left, right fleetAssignment) bool {
	return left.Box.AccountID == right.Box.AccountID && left.Box.ID == right.Box.ID && left.Box.State == right.Box.State &&
		left.Box.AssignmentGeneration == right.Box.AssignmentGeneration && left.Slot.ID == right.Slot.ID &&
		left.FencingToken == right.FencingToken && nativeFence(left) == nativeFence(right)
}
