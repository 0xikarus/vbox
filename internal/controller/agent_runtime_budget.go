package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func normalizedRunBudget(value time.Duration) time.Duration {
	if value <= 0 {
		return 8 * time.Hour
	}
	return value
}

const maxAgentRunBudgetSeconds = 30 * 24 * 60 * 60

func (s *Store) syncAgentRunBudget(ctx context.Context, accountID, boxID string, defaultBudget time.Duration) (v1.AgentRunBudget, error) {
	var result v1.AgentRunBudget
	var state string
	var generation int64
	defaultSeconds := max(int64(1), int64(normalizedRunBudget(defaultBudget)/time.Second))
	var seconds int64
	err := s.DB.QueryRowContext(ctx, `SELECT state,assignment_generation,
		COALESCE(CASE WHEN (metadata->>'runBudgetSeconds') ~ '^[0-9]{1,7}$'
			THEN CASE WHEN (metadata->>'runBudgetSeconds')::bigint<=$4
				THEN (metadata->>'runBudgetSeconds')::bigint END END,$3)
		FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state<>'deleting'`, accountID, boxID, defaultSeconds, maxAgentRunBudgetSeconds).Scan(&state, &generation, &seconds)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("box unavailable")
	}
	if err != nil {
		return result, err
	}
	deadline := any(nil)
	if state == string(v1.LogicalBoxRunning) && seconds > 0 {
		deadline = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO agent_run_budgets(account_id,box_id,assignment_generation,remaining_seconds,deadline_at)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_id,box_id) DO UPDATE SET assignment_generation=excluded.assignment_generation,
		remaining_seconds=excluded.remaining_seconds,deadline_at=excluded.deadline_at,extension_seconds=0,updated_at=now()
		WHERE agent_run_budgets.assignment_generation<>excluded.assignment_generation`, accountID, boxID, generation, seconds, deadline)
	if err != nil {
		return result, err
	}
	if state == string(v1.LogicalBoxRunning) {
		_, err = s.DB.ExecContext(ctx, `UPDATE agent_run_budgets SET deadline_at=now()+remaining_seconds*interval '1 second',updated_at=now() WHERE account_id=$1 AND box_id=$2 AND deadline_at IS NULL AND remaining_seconds>0`, accountID, boxID)
	} else {
		_, err = s.DB.ExecContext(ctx, `UPDATE agent_run_budgets SET remaining_seconds=GREATEST(0,EXTRACT(EPOCH FROM deadline_at-now())::bigint),deadline_at=NULL,updated_at=now() WHERE account_id=$1 AND box_id=$2 AND deadline_at IS NOT NULL`, accountID, boxID)
	}
	if err != nil {
		return result, err
	}
	var deadlineAt sql.NullTime
	var extensionSeconds int64
	err = s.DB.QueryRowContext(ctx, `SELECT remaining_seconds,deadline_at,extension_seconds FROM agent_run_budgets WHERE account_id=$1 AND box_id=$2`, accountID, boxID).Scan(&result.RemainingSeconds, &deadlineAt, &extensionSeconds)
	if err != nil {
		return result, err
	}
	if deadlineAt.Valid {
		value := deadlineAt.Time.UTC()
		result.DeadlineAt = &value
		result.RemainingSeconds = max(int64(0), int64(time.Until(value).Seconds()))
	}
	result.BoxID, result.State, result.BudgetSeconds, result.ExtensionUsedMinutes = boxID, state, seconds, int(extensionSeconds/60)
	return result, nil
}

func (s *Store) AgentRunBudget(ctx context.Context, accountID, boxID string, defaultBudget time.Duration) (v1.AgentRunBudget, error) {
	result, err := s.syncAgentRunBudget(ctx, accountID, boxID, defaultBudget)
	if err != nil {
		return result, err
	}
	capabilities, err := s.EffectiveAgentCapabilities(ctx, accountID, boxID)
	if err != nil {
		return result, err
	}
	result.CanRequestMoreTime = capabilities.RequestMoreTime.Enabled && result.BudgetSeconds > 0
	result.MaxExtensionMinutes = capabilities.RequestMoreTime.MaxExtensionMinutes
	result.MaxTotalMinutes = capabilities.RequestMoreTime.MaxTotalMinutes
	return result, nil
}

func (s *Store) ExtendAgentRunBudget(ctx context.Context, accountID, boxID, key string, minutes int, defaultBudget time.Duration) (v1.AgentRunBudget, error) {
	capabilities, err := s.EffectiveAgentCapabilities(ctx, accountID, boxID)
	if err != nil {
		return v1.AgentRunBudget{}, err
	}
	grant := capabilities.RequestMoreTime
	if err := requireCapability(grant.Enabled, "request_more_time"); err != nil {
		return v1.AgentRunBudget{}, err
	}
	if minutes < 1 || minutes > grant.MaxExtensionMinutes {
		return v1.AgentRunBudget{}, fmt.Errorf("minutes must be between 1 and %d", grant.MaxExtensionMinutes)
	}
	if key == "" || len(key) > 128 {
		return v1.AgentRunBudget{}, fmt.Errorf("Idempotency-Key is required")
	}
	budget, err := s.syncAgentRunBudget(ctx, accountID, boxID, defaultBudget)
	if err != nil {
		return v1.AgentRunBudget{}, err
	}
	if budget.BudgetSeconds == 0 {
		return v1.AgentRunBudget{}, fmt.Errorf("run-time limit is disabled")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return v1.AgentRunBudget{}, err
	}
	defer tx.Rollback()
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT assignment_generation FROM agent_run_budgets WHERE account_id=$1 AND box_id=$2 FOR UPDATE`, accountID, boxID).Scan(&generation); err != nil {
		return v1.AgentRunBudget{}, err
	}
	var recorded int
	err = tx.QueryRowContext(ctx, `INSERT INTO agent_run_budget_extensions(id,account_id,box_id,assignment_generation,idempotency_key,minutes) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(account_id,box_id,assignment_generation,idempotency_key) DO NOTHING RETURNING minutes`, uuid(), accountID, boxID, generation, key, minutes).Scan(&recorded)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.QueryRowContext(ctx, `SELECT minutes FROM agent_run_budget_extensions WHERE account_id=$1 AND box_id=$2 AND assignment_generation=$3 AND idempotency_key=$4`, accountID, boxID, generation, key).Scan(&recorded); err != nil {
			return v1.AgentRunBudget{}, err
		}
		if recorded != minutes {
			return v1.AgentRunBudget{}, fmt.Errorf("idempotency key was already used with different minutes")
		}
		if err := tx.Commit(); err != nil {
			return v1.AgentRunBudget{}, err
		}
		return s.AgentRunBudget(ctx, accountID, boxID, defaultBudget)
	}
	if err != nil {
		return v1.AgentRunBudget{}, err
	}
	seconds := int64(minutes * 60)
	result, err := tx.ExecContext(ctx, `UPDATE agent_run_budgets SET remaining_seconds=remaining_seconds+$3,
		deadline_at=CASE WHEN deadline_at IS NULL THEN NULL ELSE deadline_at+$3*interval '1 second' END,
		extension_seconds=extension_seconds+$3,updated_at=now() WHERE account_id=$1 AND box_id=$2 AND extension_seconds+$3<=$4`, accountID, boxID, seconds, int64(grant.MaxTotalMinutes*60))
	if err != nil {
		return v1.AgentRunBudget{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return v1.AgentRunBudget{}, fmt.Errorf("total run-time extension limit reached")
	}
	if err := tx.Commit(); err != nil {
		return v1.AgentRunBudget{}, err
	}
	return s.AgentRunBudget(ctx, accountID, boxID, defaultBudget)
}

func (s *Server) agentRunBudgetHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	boxID := r.PathValue("id")
	if r.Method == http.MethodGet {
		budget, err := s.Store.AgentRunBudget(r.Context(), p.AccountID, boxID, s.DefaultRunBudget)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, budget)
		return
	}
	var request v1.RequestMoreTimeRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	budget, err := s.Store.ExtendAgentRunBudget(r.Context(), p.AccountID, boxID, r.Header.Get("Idempotency-Key"), request.Minutes, s.DefaultRunBudget)
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	writeJSON(w, http.StatusOK, budget)
}

func (s *Server) ReconcileAgentRunBudgetsNow(ctx context.Context) error {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT account_id::text,id::text FROM logical_boxes WHERE state<>'deleting'`)
	if err != nil {
		return err
	}
	type boxKey struct{ accountID, boxID string }
	var boxes []boxKey
	for rows.Next() {
		var box boxKey
		if err := rows.Scan(&box.accountID, &box.boxID); err != nil {
			rows.Close()
			return err
		}
		boxes = append(boxes, box)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, box := range boxes {
		if _, err := s.Store.syncAgentRunBudget(ctx, box.accountID, box.boxID, s.DefaultRunBudget); err != nil {
			return err
		}
	}
	expired, err := s.Store.DB.QueryContext(ctx, `SELECT b.account_id::text,b.id::text,b.owner_user_id::text FROM agent_run_budgets rb JOIN logical_boxes b ON b.id=rb.box_id AND b.account_id=rb.account_id WHERE rb.deadline_at<=now() AND b.state='running'`)
	if err != nil {
		return err
	}
	defer expired.Close()
	for expired.Next() {
		var accountID, boxID, userID string
		if err := expired.Scan(&accountID, &boxID, &userID); err != nil {
			return err
		}
		s.startLogicalBoxHibernate(Principal{AccountID: accountID, UserID: userID, Role: "user", Subject: "controller:run-budget"}, boxID)
	}
	return expired.Err()
}
