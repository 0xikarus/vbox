package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func (s *Store) QueueAgentFollowup(ctx context.Context, p Principal, boxID, key string, request v1.QueueFollowupRequest) (v1.AgentFollowup, bool, error) {
	var value v1.AgentFollowup
	grant, err := s.EffectiveAgentCapabilities(ctx, p.AccountID, boxID)
	if err != nil {
		return value, false, err
	}
	if err := requireCapability(grant.QueueFollowup.Enabled, "queue_followup"); err != nil {
		return value, false, err
	}
	request.Text = strings.TrimSpace(request.Text)
	if key == "" || len(key) > 128 {
		return value, false, fmt.Errorf("Idempotency-Key is required")
	}
	if request.Text == "" || len(request.Text) > 100000 {
		return value, false, fmt.Errorf("follow-up text must contain between 1 and 100000 bytes")
	}
	if request.DelaySeconds < 0 || request.DelaySeconds > grant.QueueFollowup.MaxDelayMinutes*60 {
		return value, false, fmt.Errorf("delay exceeds this role's %d minute limit", grant.QueueFollowup.MaxDelayMinutes)
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return value, false, err
	}
	defer tx.Rollback()
	var generation int64
	// Lock the actor box before checking either the quota or active task. This
	// makes the pending-count decision atomic for concurrent requests.
	if err := tx.QueryRowContext(ctx, `SELECT assignment_generation FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state<>'deleting' FOR UPDATE`, p.AccountID, boxID).Scan(&generation); err != nil {
		return value, false, err
	}
	var storedDelay int
	err = tx.QueryRowContext(ctx, `SELECT id::text,text,delay_seconds,due_at,state,created_at FROM agent_followups WHERE account_id=$1 AND box_id=$2 AND idempotency_key=$3`, p.AccountID, boxID, key).Scan(&value.ID, &value.Text, &storedDelay, &value.DueAt, &value.State, &value.CreatedAt)
	if err == nil {
		if value.Text != request.Text || storedDelay != request.DelaySeconds {
			return value, true, fmt.Errorf("idempotency key was already used with different follow-up parameters")
		}
		if err := tx.Commit(); err != nil {
			return value, true, err
		}
		return value, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return value, false, err
	}
	var pending int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_followups WHERE account_id=$1 AND box_id=$2 AND state IN ('queued','delivering')`, p.AccountID, boxID).Scan(&pending); err != nil {
		return value, false, err
	}
	if pending >= int64(grant.QueueFollowup.MaxPending) {
		return value, false, fmt.Errorf("pending follow-up limit reached")
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM box_tasks WHERE account_id=$1 AND logical_box_id=$2 AND state='active' AND agent<>'shell' ORDER BY created_at DESC,id DESC LIMIT 2`, p.AccountID, boxID)
	if err != nil {
		return value, false, err
	}
	var taskIDs []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			rows.Close()
			return value, false, err
		}
		taskIDs = append(taskIDs, taskID)
	}
	if err := rows.Close(); err != nil {
		return value, false, err
	}
	if len(taskIDs) != 1 {
		return value, false, fmt.Errorf("follow-up requires exactly one active non-shell task for this box")
	}
	id := uuid()
	due := time.Now().UTC().Add(time.Duration(request.DelaySeconds) * time.Second)
	err = tx.QueryRowContext(ctx, `INSERT INTO agent_followups(id,account_id,box_id,task_id,created_by,assignment_generation,text,delay_seconds,due_at,state,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'queued',$10) ON CONFLICT(account_id,box_id,idempotency_key) DO NOTHING RETURNING id::text,text,due_at,state,created_at`, id, p.AccountID, boxID, taskIDs[0], p.UserID, generation, request.Text, request.DelaySeconds, due, key).Scan(&value.ID, &value.Text, &value.DueAt, &value.State, &value.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT id::text,text,delay_seconds,due_at,state,created_at FROM agent_followups WHERE account_id=$1 AND box_id=$2 AND idempotency_key=$3`, p.AccountID, boxID, key).Scan(&value.ID, &value.Text, &storedDelay, &value.DueAt, &value.State, &value.CreatedAt)
		if err == nil && (value.Text != request.Text || storedDelay != request.DelaySeconds) {
			err = fmt.Errorf("idempotency key was already used with different follow-up parameters")
		}
		if err != nil {
			return value, true, err
		}
		if err := tx.Commit(); err != nil {
			return value, true, err
		}
		return value, true, nil
	}
	if err != nil {
		return value, false, err
	}
	if err := tx.Commit(); err != nil {
		return value, false, err
	}
	return value, false, nil
}

func (s *Server) agentFollowupHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var request v1.QueueFollowupRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	value, reused, err := s.Store.QueueAgentFollowup(r.Context(), p, r.PathValue("id"), r.Header.Get("Idempotency-Key"), request)
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if reused {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(w, http.StatusAccepted, value)
}

func (s *Server) ReconcileAgentFollowupsNow(ctx context.Context) error {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT f.account_id::text,f.id::text,f.box_id::text,f.created_by::text,f.text,COALESCE(t.id::text,''),COALESCE(t.state,''),COALESCE(t.agent,'') FROM agent_followups f JOIN logical_boxes b ON b.id=f.box_id AND b.account_id=f.account_id LEFT JOIN box_tasks t ON t.id=f.task_id AND t.account_id=f.account_id AND t.logical_box_id=f.box_id WHERE f.state='queued' AND f.due_at<=now() AND b.state='running' AND b.assignment_generation=f.assignment_generation ORDER BY f.due_at,f.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type due struct{ account, id, box, user, text, task, taskState, agent string }
	var values []due
	for rows.Next() {
		var value due
		if err := rows.Scan(&value.account, &value.id, &value.box, &value.user, &value.text, &value.task, &value.taskState, &value.agent); err != nil {
			return err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, value := range values {
		if value.task == "" || value.taskState != "active" || value.agent == "shell" {
			_, err := s.Store.DB.ExecContext(ctx, `UPDATE agent_followups SET state='canceled',failure_reason='bound interactive task is no longer active',updated_at=now() WHERE account_id=$1 AND id=$2 AND state='queued'`, value.account, value.id)
			if err != nil {
				return err
			}
			continue
		}
		capabilities, capErr := s.Store.EffectiveAgentCapabilities(ctx, value.account, value.box)
		if capErr != nil {
			return capErr
		}
		if !capabilities.QueueFollowup.Enabled {
			_, err := s.Store.DB.ExecContext(ctx, `UPDATE agent_followups SET state='canceled',failure_reason='queue_followup permission was revoked',updated_at=now() WHERE account_id=$1 AND id=$2 AND state='queued'`, value.account, value.id)
			if err != nil {
				return err
			}
			continue
		}
		result, err := s.Store.DB.ExecContext(ctx, `UPDATE agent_followups SET state='delivering',updated_at=now() WHERE account_id=$1 AND id=$2 AND state='queued'`, value.account, value.id)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			continue
		}
		messageID := uuid()
		_, err = s.Store.DB.ExecContext(ctx, `INSERT INTO box_messages(id,account_id,task_id,user_id,direction,body,submit,state,idempotency_key,chat_key,thread_id) VALUES($1,$2,$3,$4,'system',$5,true,'queued',$6,$7,$1) ON CONFLICT(account_id,idempotency_key) DO NOTHING`, messageID, value.account, value.task, value.user, value.text, "agent-followup:"+value.id, chatMessageKey())
		if err != nil {
			_, _ = s.Store.DB.ExecContext(ctx, `UPDATE agent_followups SET state='failed',failure_reason=$3,updated_at=now() WHERE account_id=$1 AND id=$2`, value.account, value.id, err.Error())
			continue
		}
		_, err = s.Store.DB.ExecContext(ctx, `UPDATE agent_followups SET state='delivered',updated_at=now() WHERE account_id=$1 AND id=$2`, value.account, value.id)
		if err != nil {
			return err
		}
	}
	return nil
}
