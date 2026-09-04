package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

const boxTaskSelect = "SELECT t.id::text,t.logical_box_id::text,b.name,t.user_id::text,t.requested_role,t.agent,t.session_name,t.prompt,t.state,COALESCE(t.failure_reason,''),t.created_at,t.updated_at FROM box_tasks t JOIN logical_boxes b ON b.id=t.logical_box_id"

func scanBoxTask(scanner interface{ Scan(...any) error }) (v1.BoxTask, error) {
	var task v1.BoxTask
	err := scanner.Scan(&task.ID, &task.LogicalBoxID, &task.BoxName, &task.UserID, &task.RequestedRole, &task.Agent, &task.Session, &task.Prompt, &task.State, &task.Failure, &task.CreatedAt, &task.UpdatedAt)
	return task, err
}

func validAgent(value string) bool {
	switch value {
	case "codex", "claude", "opencode", "shell":
		return true
	default:
		return false
	}
}

func validSessionName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func generatedTaskSession(agent string) string {
	id := strings.ReplaceAll(uuid(), "-", "")
	return agent + "-" + id[:12]
}

func (s *Store) CreateBoxTask(ctx context.Context, p Principal, logicalBoxID, idempotency string, request v1.CreateBoxTaskRequest) (v1.BoxTask, bool, error) {
	if idempotency == "" {
		return v1.BoxTask{}, false, fmt.Errorf("Idempotency-Key is required")
	}
	request.Agent = strings.ToLower(strings.TrimSpace(request.Agent))
	if request.Agent == "" {
		request.Agent = "codex"
	}
	if !validAgent(request.Agent) {
		return v1.BoxTask{}, false, fmt.Errorf("agent must be codex, claude, opencode, or shell")
	}
	if request.Session == "" {
		request.Session = generatedTaskSession(request.Agent)
	}
	if !validSessionName(request.Session) {
		return v1.BoxTask{}, false, fmt.Errorf("session must contain only letters, digits, hyphen, or underscore")
	}
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.Prompt == "" || len(request.Prompt) > 100_000 {
		return v1.BoxTask{}, false, fmt.Errorf("prompt must contain between 1 and 100000 bytes")
	}
	box, err := s.LogicalBox(ctx, p, logicalBoxID)
	if err != nil {
		return v1.BoxTask{}, false, err
	}
	if task, err := scanBoxTask(s.DB.QueryRowContext(ctx, boxTaskSelect+" WHERE t.account_id=$1 AND t.idempotency_key=$2", p.AccountID, idempotency)); err == nil {
		return task, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return v1.BoxTask{}, false, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return v1.BoxTask{}, false, err
	}
	defer tx.Rollback()
	taskID, messageID := uuid(), uuid()
	task, err := scanBoxTask(tx.QueryRowContext(ctx, "INSERT INTO box_tasks(id,account_id,logical_box_id,user_id,requested_role,agent,session_name,prompt,state,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'queued',$9) ON CONFLICT(account_id,idempotency_key) DO NOTHING RETURNING id::text,logical_box_id::text,$10,user_id::text,requested_role,agent,session_name,prompt,state,COALESCE(failure_reason,''),created_at,updated_at", taskID, p.AccountID, box.ID, p.UserID, p.Role, request.Agent, request.Session, request.Prompt, idempotency, box.Name))
	if errors.Is(err, sql.ErrNoRows) {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return v1.BoxTask{}, false, rollbackErr
		}
		task, err = scanBoxTask(s.DB.QueryRowContext(ctx, boxTaskSelect+" WHERE t.account_id=$1 AND t.idempotency_key=$2", p.AccountID, idempotency))
		return task, true, err
	}
	if err != nil {
		return v1.BoxTask{}, false, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO box_messages(id,account_id,task_id,user_id,direction,body,submit,state,idempotency_key) VALUES($1,$2,$3,$4,'user',$5,true,'queued',$6)", messageID, p.AccountID, task.ID, p.UserID, request.Prompt, idempotency+":initial"); err != nil {
		return v1.BoxTask{}, false, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'logical_box.task.create','box_task',$3,jsonb_build_object('logical_box_id',$4::text,'agent',$5::text))", p.AccountID, p.UserID, task.ID, box.ID, task.Agent); err != nil {
		return v1.BoxTask{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return v1.BoxTask{}, false, err
	}
	return task, false, nil
}

func (s *Store) BoxTask(ctx context.Context, p Principal, id string) (v1.BoxTask, error) {
	task, err := scanBoxTask(s.DB.QueryRowContext(ctx, boxTaskSelect+" WHERE t.account_id=$1 AND t.id=$2 AND (b.owner_user_id=$3 OR $4='owner')", p.AccountID, id, p.UserID, p.Role))
	if errors.Is(err, sql.ErrNoRows) {
		return task, fmt.Errorf("task not found")
	}
	return task, err
}

func (s *Store) ListBoxTasks(ctx context.Context, p Principal, logicalBoxID string) ([]v1.BoxTask, error) {
	box, err := s.LogicalBox(ctx, p, logicalBoxID)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, boxTaskSelect+" WHERE t.account_id=$1 AND t.logical_box_id=$2 ORDER BY t.created_at,t.id", p.AccountID, box.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []v1.BoxTask
	for rows.Next() {
		task, err := scanBoxTask(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, task)
	}
	return values, rows.Err()
}

type runnableBoxTask struct {
	AccountID string
	Task      v1.BoxTask
}

func (s *Store) RunnableBoxTasks(ctx context.Context) ([]runnableBoxTask, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT account_id::text,id::text FROM box_tasks WHERE state IN ('queued','waiting_capacity','starting') ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ accountID, id string }
	var keys []key
	for rows.Next() {
		var value key
		if err := rows.Scan(&value.accountID, &value.id); err != nil {
			return nil, err
		}
		keys = append(keys, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	values := make([]runnableBoxTask, 0, len(keys))
	for _, value := range keys {
		task, err := scanBoxTask(s.DB.QueryRowContext(ctx, boxTaskSelect+" WHERE t.account_id=$1 AND t.id=$2", value.accountID, value.id))
		if err != nil {
			return nil, err
		}
		values = append(values, runnableBoxTask{AccountID: value.accountID, Task: task})
	}
	return values, nil
}

func (s *Store) SetBoxTaskState(ctx context.Context, accountID, id, state, failure string) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE box_tasks SET state=$3,failure_reason=NULLIF($4,''),updated_at=now() WHERE account_id=$1 AND id=$2", accountID, id, state, failure)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("task not found")
	}
	return nil
}

const boxMessageSelect = "SELECT id::text,task_id::text,COALESCE(user_id::text,''),direction,body,state,created_at,updated_at FROM box_messages"

func scanBoxMessage(scanner interface{ Scan(...any) error }) (v1.BoxMessage, error) {
	var message v1.BoxMessage
	err := scanner.Scan(&message.ID, &message.TaskID, &message.UserID, &message.Direction, &message.Text, &message.State, &message.CreatedAt, &message.UpdatedAt)
	return message, err
}

func (s *Store) CreateBoxMessage(ctx context.Context, p Principal, taskID, idempotency string, request v1.SendBoxMessageRequest) (v1.BoxMessage, bool, error) {
	if idempotency == "" {
		return v1.BoxMessage{}, false, fmt.Errorf("Idempotency-Key is required")
	}
	if _, err := s.BoxTask(ctx, p, taskID); err != nil {
		return v1.BoxMessage{}, false, err
	}
	if request.Text == "" || len(request.Text) > 100_000 {
		return v1.BoxMessage{}, false, fmt.Errorf("message must contain between 1 and 100000 bytes")
	}
	submit := true
	if request.Submit != nil {
		submit = *request.Submit
	}
	messageID := uuid()
	message, err := scanBoxMessage(s.DB.QueryRowContext(ctx, "INSERT INTO box_messages(id,account_id,task_id,user_id,direction,body,submit,state,idempotency_key) VALUES($1,$2,$3,$4,'user',$5,$6,'queued',$7) ON CONFLICT(account_id,idempotency_key) DO NOTHING RETURNING id::text,task_id::text,user_id::text,direction,body,state,created_at,updated_at", messageID, p.AccountID, taskID, p.UserID, request.Text, submit, idempotency))
	if errors.Is(err, sql.ErrNoRows) {
		message, err = scanBoxMessage(s.DB.QueryRowContext(ctx, boxMessageSelect+" WHERE account_id=$1 AND idempotency_key=$2", p.AccountID, idempotency))
		return message, true, err
	}
	return message, false, err
}

func (s *Store) ListBoxMessages(ctx context.Context, p Principal, taskID string) ([]v1.BoxMessage, error) {
	if _, err := s.BoxTask(ctx, p, taskID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, boxMessageSelect+" WHERE account_id=$1 AND task_id=$2 ORDER BY created_at,id", p.AccountID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []v1.BoxMessage
	for rows.Next() {
		message, err := scanBoxMessage(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, message)
	}
	return values, rows.Err()
}

func (s *Store) ClaimBoxMessage(ctx context.Context, accountID, id string) (bool, error) {
	result, err := s.DB.ExecContext(ctx, "UPDATE box_messages SET state='delivering',failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND state='queued'", accountID, id)
	if err != nil {
		return false, err
	}
	changed, _ := result.RowsAffected()
	return changed == 1, nil
}

func (s *Store) SetBoxMessageState(ctx context.Context, accountID, id, state, failure string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE box_messages SET state=$3,failure_reason=NULLIF($4,''),updated_at=now() WHERE account_id=$1 AND id=$2", accountID, id, state, failure)
	return err
}

func (s *Store) RecoverStaleBoxMessages(ctx context.Context, before time.Time) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE box_messages SET state='ambiguous',failure_reason='delivery was interrupted; inspect the terminal before retrying',updated_at=now() WHERE state='delivering' AND updated_at < $1", before)
	return err
}

func (s *Store) FirstQueuedTaskMessage(ctx context.Context, accountID, taskID string) (v1.BoxMessage, bool, bool, error) {
	var submit bool
	message, err := scanBoxMessageWithSubmit(s.DB.QueryRowContext(ctx, "SELECT id::text,task_id::text,COALESCE(user_id::text,''),direction,body,state,created_at,updated_at,submit FROM box_messages WHERE account_id=$1 AND task_id=$2 AND state='queued' ORDER BY created_at,id LIMIT 1", accountID, taskID), &submit)
	if errors.Is(err, sql.ErrNoRows) {
		return message, false, false, nil
	}
	return message, submit, err == nil, err
}

func scanBoxMessageWithSubmit(scanner interface{ Scan(...any) error }, submit *bool) (v1.BoxMessage, error) {
	var message v1.BoxMessage
	err := scanner.Scan(&message.ID, &message.TaskID, &message.UserID, &message.Direction, &message.Text, &message.State, &message.CreatedAt, &message.UpdatedAt, submit)
	return message, err
}

func (s *Store) ClaimBoxTask(ctx context.Context, accountID, id string) (bool, error) {
	result, err := s.DB.ExecContext(ctx, "UPDATE box_tasks SET state='starting',failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2 AND (state IN ('queued','waiting_capacity') OR (state='starting' AND updated_at < now()-interval '2 minutes'))", accountID, id)
	if err != nil {
		return false, err
	}
	changed, _ := result.RowsAffected()
	return changed == 1, nil
}

type queuedBoxMessage struct {
	AccountID string
	Task      v1.BoxTask
	Message   v1.BoxMessage
	Submit    bool
}

func (s *Store) QueuedActiveBoxMessages(ctx context.Context) ([]queuedBoxMessage, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT m.account_id::text,m.id::text,m.task_id::text,m.submit FROM box_messages m JOIN box_tasks t ON t.id=m.task_id AND t.account_id=m.account_id WHERE m.state='queued' AND t.state='active' ORDER BY m.created_at,m.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct {
		accountID string
		messageID string
		taskID    string
		submit    bool
	}
	var keys []key
	for rows.Next() {
		var value key
		if err := rows.Scan(&value.accountID, &value.messageID, &value.taskID, &value.submit); err != nil {
			return nil, err
		}
		keys = append(keys, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	values := make([]queuedBoxMessage, 0, len(keys))
	for _, value := range keys {
		task, err := scanBoxTask(s.DB.QueryRowContext(ctx, boxTaskSelect+" WHERE t.account_id=$1 AND t.id=$2", value.accountID, value.taskID))
		if err != nil {
			return nil, err
		}
		message, err := scanBoxMessage(s.DB.QueryRowContext(ctx, boxMessageSelect+" WHERE account_id=$1 AND id=$2", value.accountID, value.messageID))
		if err != nil {
			return nil, err
		}
		values = append(values, queuedBoxMessage{AccountID: value.accountID, Task: task, Message: message, Submit: value.submit})
	}
	return values, nil
}
