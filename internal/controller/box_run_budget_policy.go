package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

type boxRunBudgetPolicyResponse struct {
	Seconds          int64      `json:"seconds"`
	RemainingSeconds int64      `json:"remainingSeconds"`
	DeadlineAt       *time.Time `json:"deadlineAt,omitempty"`
	RunningSince     *time.Time `json:"runningSince,omitempty"`
	State            string     `json:"state"`
}

func (s *Server) boxRunBudgetPolicy(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("box unavailable"))
		return
	}
	if r.Method == http.MethodPut {
		var request struct {
			Seconds *int64 `json:"seconds"`
		}
		if err := decodeJSON(r, &request); err != nil || request.Seconds == nil || *request.Seconds < 0 || *request.Seconds > maxAgentRunBudgetSeconds || (*request.Seconds > 0 && *request.Seconds < 60) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("choose 0 to disable or 60–%d seconds", maxAgentRunBudgetSeconds))
			return
		}
		if err := s.Store.SetBoxRunBudget(r.Context(), p.AccountID, box.ID, *request.Seconds); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
	} else if r.Method == http.MethodPost {
		var request struct {
			Action             string     `json:"action"`
			Seconds            int64      `json:"seconds"`
			ExpectedDeadlineAt *time.Time `json:"expectedDeadlineAt"`
		}
		if err := decodeJSON(r, &request); err != nil || request.ExpectedDeadlineAt == nil || (request.Action != "reset" && request.Action != "add") ||
			(request.Action == "reset" && request.Seconds != 0) ||
			(request.Action == "add" && request.Seconds != 4*3600 && request.Seconds != 8*3600 && request.Seconds != 24*3600) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("choose reset or add 4, 8, or 24 hours"))
			return
		}
		if err := s.Store.AdjustBoxRunBudget(r.Context(), p.AccountID, box.ID, request.Action, request.Seconds, *request.ExpectedDeadlineAt, s.DefaultRunBudget); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
	}
	budget, err := s.Store.syncAgentRunBudget(r.Context(), p.AccountID, box.ID, s.DefaultRunBudget)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("run-time limit unavailable"))
		return
	}
	var runningSince *time.Time
	if box.State == v1.LogicalBoxRunning {
		var started sql.NullTime
		err = s.Store.DB.QueryRowContext(r.Context(), `SELECT MAX(updated_at) FROM allocation_requests
			WHERE account_id=$1 AND logical_box_id=$2 AND assignment_generation=$3 AND state='ready'`, p.AccountID, box.ID, box.AssignmentGeneration).Scan(&started)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("current run time unavailable"))
			return
		}
		if started.Valid {
			value := started.Time.UTC()
			runningSince = &value
		}
	}
	writeJSON(w, http.StatusOK, boxRunBudgetPolicyResponse{
		Seconds: budget.BudgetSeconds, RemainingSeconds: budget.RemainingSeconds,
		DeadlineAt: budget.DeadlineAt, RunningSince: runningSince, State: budget.State,
	})
}

// AdjustBoxRunBudget changes only the current allocation's countdown. The
// configured limit remains the default for the next allocation.
func (s *Store) AdjustBoxRunBudget(ctx context.Context, accountID, boxID, action string, addSeconds int64, expectedDeadline time.Time, defaultBudget time.Duration) error {
	if (action != "reset" && action != "add") || (action == "reset" && addSeconds != 0) ||
		(action == "add" && addSeconds != 4*3600 && addSeconds != 8*3600 && addSeconds != 24*3600) {
		return fmt.Errorf("invalid run-time adjustment")
	}
	if _, err := s.syncAgentRunBudget(ctx, accountID, boxID, defaultBudget); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	defaultSeconds := max(int64(1), int64(normalizedRunBudget(defaultBudget)/time.Second))
	var state string
	var generation, configuredSeconds int64
	err = tx.QueryRowContext(ctx, `SELECT state,assignment_generation,
		COALESCE(CASE WHEN (metadata->>'runBudgetSeconds') ~ '^[0-9]{1,7}$'
			THEN CASE WHEN (metadata->>'runBudgetSeconds')::bigint<=$4
				THEN (metadata->>'runBudgetSeconds')::bigint END END,$3)
		FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state<>'deleting' FOR UPDATE`, accountID, boxID, defaultSeconds, maxAgentRunBudgetSeconds).Scan(&state, &generation, &configuredSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("box unavailable")
	}
	if err != nil {
		return err
	}
	if state != string(v1.LogicalBoxRunning) {
		return fmt.Errorf("box is not running")
	}
	if configuredSeconds == 0 {
		return fmt.Errorf("run-time limit is off")
	}
	var deadline sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT deadline_at FROM agent_run_budgets WHERE account_id=$1 AND box_id=$2 AND assignment_generation=$3 FOR UPDATE`, accountID, boxID, generation).Scan(&deadline); err != nil || !deadline.Valid {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return fmt.Errorf("run-time countdown unavailable")
	}
	if !deadline.Time.Equal(expectedDeadline) {
		return fmt.Errorf("run-time countdown changed; refresh and try again")
	}
	now := time.Now().UTC()
	remaining := configuredSeconds
	if action == "add" {
		remaining = max(int64(0), int64(math.Ceil(deadline.Time.Sub(now).Seconds())))
		if remaining > maxAgentRunBudgetSeconds-addSeconds {
			return fmt.Errorf("run-time countdown cannot exceed 30 days")
		}
		remaining += addSeconds
	}
	result, err := tx.ExecContext(ctx, `UPDATE agent_run_budgets SET remaining_seconds=$4,deadline_at=$5,
		extension_seconds=CASE WHEN $6 THEN 0 ELSE extension_seconds END,updated_at=now()
		WHERE account_id=$1 AND box_id=$2 AND assignment_generation=$3`, accountID, boxID, generation, remaining, now.Add(time.Duration(remaining)*time.Second), action == "reset")
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("run-time countdown changed")
	}
	return tx.Commit()
}

// SetBoxRunBudget sets the limit for future allocations and starts a new
// countdown for the current allocation. Zero disables the deadline.
func (s *Store) SetBoxRunBudget(ctx context.Context, accountID, boxID string, seconds int64) error {
	if seconds < 0 || seconds > maxAgentRunBudgetSeconds || (seconds > 0 && seconds < 60) {
		return fmt.Errorf("invalid run-time limit")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT state,assignment_generation FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state<>'deleting' FOR UPDATE`, accountID, boxID).Scan(&state, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("box unavailable")
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE logical_boxes SET metadata=jsonb_set(metadata,'{runBudgetSeconds}',to_jsonb($3::bigint),true) WHERE account_id=$1 AND id=$2`, accountID, boxID, seconds); err != nil {
		return err
	}
	var deadline any
	if state == string(v1.LogicalBoxRunning) && seconds > 0 {
		deadline = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_run_budgets(account_id,box_id,assignment_generation,remaining_seconds,deadline_at)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_id,box_id) DO UPDATE SET
		assignment_generation=excluded.assignment_generation,remaining_seconds=excluded.remaining_seconds,
		deadline_at=excluded.deadline_at,extension_seconds=0,updated_at=now()`, accountID, boxID, generation, seconds, deadline); err != nil {
		return err
	}
	return tx.Commit()
}
