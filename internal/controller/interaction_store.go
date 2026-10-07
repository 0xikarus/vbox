package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
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

type sqlStateError interface {
	SQLState() string
}

func serializationFailure(err error) bool {
	var state sqlStateError
	return errors.As(err, &state) && state.SQLState() == "40001"
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
	return s.createBoxTaskWithRetry(ctx, p, box, idempotency, request)
}

func (s *Store) createBoxTaskWithRetry(ctx context.Context, p Principal, box v1.LogicalBox, idempotency string, request v1.CreateBoxTaskRequest) (v1.BoxTask, bool, error) {
	var task v1.BoxTask
	var reused bool
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		task, reused, err = s.createBoxTaskTransaction(ctx, p, box, idempotency, request)
		if !serializationFailure(err) {
			return task, reused, err
		}
		if ctx.Err() != nil {
			return task, reused, ctx.Err()
		}
	}
	return task, reused, err
}

func (s *Store) createBoxTaskTransaction(ctx context.Context, p Principal, box v1.LogicalBox, idempotency string, request v1.CreateBoxTaskRequest) (v1.BoxTask, bool, error) {
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
	direction, senderBoxID := boxMessageOrigin(request.SenderBoxID)
	parentID, threadID, err := resolveMessageThread(ctx, tx, p.AccountID, box.ID, messageID, request.ParentMessageID)
	if err != nil {
		return v1.BoxTask{}, false, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO box_messages(id,account_id,task_id,user_id,direction,body,submit,state,idempotency_key,chat_key,sender_box_id,parent_message_id,thread_id) VALUES($1,$2,$3,$4,$5,$6,true,'queued',$7,$8,$9,NULLIF($10,'')::uuid,$11)", messageID, p.AccountID, task.ID, p.UserID, direction, request.Prompt, idempotency+":initial", chatMessageKey(), senderBoxID, parentID, threadID); err != nil {
		return v1.BoxTask{}, false, err
	}
	if err := attachBoxMessageImages(ctx, tx, p.AccountID, messageID, request.Images); err != nil {
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
	return s.boxTasksForReconciliation(ctx, "SELECT account_id::text,id::text FROM box_tasks WHERE state IN ('queued','waiting_capacity','starting') ORDER BY created_at,id")
}

func (s *Store) activeBoxTasks(ctx context.Context) ([]runnableBoxTask, error) {
	return s.boxTasksForReconciliation(ctx, "SELECT t.account_id::text,t.id::text FROM box_tasks t JOIN logical_boxes b ON b.id=t.logical_box_id WHERE t.state='active' AND b.state='running' ORDER BY t.updated_at,t.id")
}

func (s *Store) boxTasksForReconciliation(ctx context.Context, query string) ([]runnableBoxTask, error) {
	rows, err := s.DB.QueryContext(ctx, query)
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

// Fence the observed absence against a concurrent hibernate/restore or slot move.
// Only the observed task may change; other sessions and prompts are untouched.
func (s *Store) failMissingTask(ctx context.Context, accountID string, task v1.BoxTask, box v1.LogicalBox) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE box_tasks t SET state='failed',failure_reason='tmux session exited; start a new session to continue',updated_at=now()
FROM logical_boxes b WHERE t.account_id=$1 AND t.id=$2 AND t.state='active'
AND b.id=t.logical_box_id AND b.state='running' AND b.slot_id=$3 AND b.assignment_generation=$4`, accountID, task.ID, box.SlotID, box.AssignmentGeneration)
	return err
}

func (s *Store) SetBoxTaskState(ctx context.Context, accountID, id, state, failure string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE box_tasks SET state=$3,failure_reason=NULLIF($4,''),updated_at=now(),
		agent_busy=CASE WHEN $3='active' AND agent<>'shell' THEN COALESCE(agent_busy,true) WHEN $3<>'active' THEN NULL ELSE agent_busy END,
		agent_busy_updated_at=CASE WHEN $3='active' AND agent<>'shell' AND agent_busy IS NULL THEN now() WHEN $3<>'active' THEN NULL ELSE agent_busy_updated_at END,
		agent_busy_message_id=CASE WHEN $3<>'active' THEN NULL ELSE agent_busy_message_id END
		WHERE account_id=$1 AND id=$2`, accountID, id, state, failure)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("task not found")
	}
	return nil
}

// SetBoxTaskBusy records explicit activity for one active managed-agent task.
func (s *Store) SetBoxTaskBusy(ctx context.Context, accountID, taskID string, busy bool) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE box_tasks SET agent_busy=$3,agent_busy_updated_at=now(),agent_busy_message_id=NULL
		WHERE account_id=$1 AND id=$2 AND agent<>'shell' AND (state='active' OR NOT $3)`, accountID, taskID, busy)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("active agent chat task not found")
	}
	return nil
}

// SetBoxTaskIdleForMessage clears activity only when messageID is still the
// newest submitted prompt. A reply to an older prompt is a successful no-op:
// the agent may already be processing a newer message on the same task.
func (s *Store) SetBoxTaskIdleForMessage(ctx context.Context, accountID, taskID, messageID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE box_tasks SET agent_busy=false,agent_busy_updated_at=now(),agent_busy_message_id=NULL
		WHERE account_id=$1 AND id=$2 AND agent<>'shell' AND agent_busy_message_id=$3`, accountID, taskID, messageID)
	return err
}

// SetBoxSessionBusy is the assignment-scoped form used by the desktop agent.
func (s *Store) SetBoxSessionBusy(ctx context.Context, accountID, boxID, session string, busy bool) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE box_tasks SET agent_busy=$4,agent_busy_updated_at=now(),agent_busy_message_id=NULL
		WHERE account_id=$1 AND logical_box_id=$2 AND session_name=$3 AND state='active' AND agent<>'shell'`, accountID, boxID, session, busy)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("active agent chat session not found")
	}
	if busy {
		// This event is emitted when the agent starts a turn. Only messages
		// already handed to this session can be marked read; the state update
		// also upgrades uncertain handoffs and cannot be undone by a late receipt.
		_, err = s.DB.ExecContext(ctx, `UPDATE box_messages m SET state='read',failure_reason=NULL,updated_at=now()
			FROM box_tasks t WHERE m.account_id=$1 AND m.task_id=t.id AND t.account_id=$1
			AND t.logical_box_id=$2 AND t.session_name=$3 AND t.state='active'
			AND m.direction IN ('user','box') AND m.submit AND m.state IN ('delivering','delivered','ambiguous')
			AND m.created_at<=t.agent_busy_updated_at`, accountID, boxID, session)
		if err != nil {
			return err
		}
	}
	return nil
}

// BoxAgentBusy returns the newest active managed chat's activity state. A box
// with no active managed chat is known idle; NULL preserves the legacy UI
// heuristic until an old task receives a new message or reports set_busy.
func (s *Store) BoxAgentBusy(ctx context.Context, accountID, boxID string) (busy, known bool, updatedAt time.Time, err error) {
	var value sql.NullBool
	var updated sql.NullTime
	err = s.DB.QueryRowContext(ctx, `SELECT agent_busy,agent_busy_updated_at FROM box_tasks
		WHERE account_id=$1 AND logical_box_id=$2 AND state='active' AND agent<>'shell'
		ORDER BY created_at DESC,id DESC LIMIT 1`, accountID, boxID).Scan(&value, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return false, true, time.Time{}, nil
	}
	if err != nil {
		return false, false, time.Time{}, err
	}
	if !value.Valid {
		return false, false, time.Time{}, nil
	}
	if updated.Valid {
		updatedAt = updated.Time
	}
	return value.Bool, true, updatedAt, nil
}

const boxMessageColumns = "id::text,task_id::text,COALESCE(user_id::text,''),direction,body,state,created_at,updated_at,COALESCE(chat_key,''),COALESCE(sender_box_id::text,''),COALESCE(parent_message_id::text,''),COALESCE(thread_id,id)::text"

const boxMessageSelect = "SELECT " + boxMessageColumns + " FROM box_messages"

// chatMessageKey is the short reference handed to the agent in the chat
// envelope. The agent echoes it as replyTo, so it stays well below the 36
// character identifier while remaining unique per account.
func chatMessageKey() string {
	raw := boxruntime.ID("")
	if len(raw) <= 12 {
		return raw
	}
	return raw[:12]
}

func scanBoxMessage(scanner interface{ Scan(...any) error }) (v1.BoxMessage, error) {
	var message v1.BoxMessage
	err := scanner.Scan(&message.ID, &message.TaskID, &message.UserID, &message.Direction, &message.Text, &message.State, &message.CreatedAt, &message.UpdatedAt, &message.ChatKey, &message.SenderBoxID, &message.ParentMessageID, &message.ThreadID)
	decodeBoxMessageQuestion(&message)
	decodeBoxMessageCaptcha(&message)
	decodeBoxMessageMail(&message)
	decodeBoxMessageControl(&message)
	return message, err
}

// BoxMessageByChatKey resolves a replyTo reference inside one task. Newer
// messages carry the short chat key; conversations started before that still
// echo the identifier.
func (s *Store) BoxMessageByChatKey(ctx context.Context, accountID, taskID, key string) (v1.BoxMessage, bool, error) {
	if key == "" || len(key) > 128 {
		return v1.BoxMessage{}, false, nil
	}
	message, err := scanBoxMessage(s.DB.QueryRowContext(ctx, boxMessageSelect+" WHERE account_id=$1 AND task_id=$2 AND (chat_key=$3 OR id::text=$3)", accountID, taskID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return message, false, nil
	}
	return message, err == nil, err
}

// BoxMessageOrdinal returns the 1-based position of a chat message inside its
// task, counting the messages an agent is expected to answer. The chat envelope
// uses it to repeat itself on the first message and then periodically.
func (s *Store) BoxMessageOrdinal(ctx context.Context, accountID, taskID, messageID string) (int, error) {
	var createdAt time.Time
	if err := s.DB.QueryRowContext(ctx, "SELECT created_at FROM box_messages WHERE account_id=$1 AND id::text=$2", accountID, messageID).Scan(&createdAt); err != nil {
		return 0, err
	}
	var ordinal int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM box_messages
		WHERE account_id=$1 AND task_id=$2 AND direction IN ('user','system')
		AND (created_at < $3 OR (created_at = $3 AND id::text <= $4))`, accountID, taskID, createdAt, messageID).Scan(&ordinal)
	return ordinal, err
}

// InsertAgentBoxMessage stores an uncorrelated agent message. eventKey makes
// repeated deliveries of the same outbox event idempotent.
func (s *Store) InsertAgentBoxMessage(ctx context.Context, accountID, taskID, eventKey, text string) (v1.BoxMessage, bool, error) {
	if strings.TrimSpace(text) == "" || len(text) > 100_000 {
		return v1.BoxMessage{}, false, fmt.Errorf("agent message must contain between 1 and 100000 bytes")
	}
	messageID := uuid()
	message, err := scanBoxMessage(s.DB.QueryRowContext(ctx, "INSERT INTO box_messages(id,account_id,task_id,direction,body,submit,state,idempotency_key,thread_id) VALUES($1,$2,$3,'agent',$4,false,'delivered',$5,$1) ON CONFLICT(account_id,idempotency_key) DO NOTHING RETURNING "+boxMessageColumns, messageID, accountID, taskID, text, "agent-message:"+eventKey))
	if errors.Is(err, sql.ErrNoRows) {
		existing, lookupErr := scanBoxMessage(s.DB.QueryRowContext(ctx, boxMessageSelect+" WHERE account_id=$1 AND idempotency_key=$2", accountID, "agent-message:"+eventKey))
		if lookupErr != nil {
			return v1.BoxMessage{}, false, lookupErr
		}
		return existing, false, nil
	}
	if err != nil {
		return v1.BoxMessage{}, false, err
	}
	return message, true, nil
}

// boxMessageOrigin maps an inter-box sender to the stored direction and origin
// column. A normal owner/agent message has no sender box.
func boxMessageOrigin(senderBoxID string) (string, any) {
	if senderBoxID != "" {
		return "box", senderBoxID
	}
	return "user", nil
}

type messageThreadQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func resolveMessageThread(ctx context.Context, q messageThreadQuery, accountID, boxID, messageID, parentMessageID string) (string, string, error) {
	parentMessageID = strings.TrimSpace(parentMessageID)
	if parentMessageID == "" {
		return "", messageID, nil
	}
	var threadID string
	err := q.QueryRowContext(ctx, `SELECT COALESCE(m.thread_id,m.id)::text FROM box_messages m
		JOIN box_tasks t ON t.id=m.task_id AND t.account_id=m.account_id
		WHERE m.account_id=$1 AND m.id::text=$2 AND t.logical_box_id=$3`, accountID, parentMessageID, boxID).Scan(&threadID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("parent message is not in this box conversation")
	}
	return parentMessageID, threadID, err
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
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return v1.BoxMessage{}, false, err
	}
	defer tx.Rollback()
	messageID := uuid()
	direction, senderBoxID := boxMessageOrigin(request.SenderBoxID)
	var boxID string
	if err := tx.QueryRowContext(ctx, `SELECT logical_box_id::text FROM box_tasks WHERE account_id=$1 AND id=$2`, p.AccountID, taskID).Scan(&boxID); err != nil {
		return v1.BoxMessage{}, false, err
	}
	parentID, threadID, err := resolveMessageThread(ctx, tx, p.AccountID, boxID, messageID, request.ParentMessageID)
	if err != nil {
		return v1.BoxMessage{}, false, err
	}
	message, err := scanBoxMessage(tx.QueryRowContext(ctx, "INSERT INTO box_messages(id,account_id,task_id,user_id,direction,body,submit,state,idempotency_key,chat_key,sender_box_id,parent_message_id,thread_id) VALUES($1,$2,$3,$4,$5,$6,$7,'queued',$8,$9,$10,NULLIF($11,'')::uuid,$12) ON CONFLICT(account_id,idempotency_key) DO NOTHING RETURNING "+boxMessageColumns, messageID, p.AccountID, taskID, p.UserID, direction, request.Text, submit, idempotency, chatMessageKey(), senderBoxID, parentID, threadID))
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Rollback(); err != nil {
			return v1.BoxMessage{}, false, err
		}
		message, err = scanBoxMessage(s.DB.QueryRowContext(ctx, boxMessageSelect+" WHERE account_id=$1 AND idempotency_key=$2", p.AccountID, idempotency))
		if err == nil {
			values := []v1.BoxMessage{message}
			err = s.loadBoxMessageImages(ctx, p.AccountID, values)
			message = values[0]
		}
		return message, true, err
	}
	if err != nil {
		return message, false, err
	}
	if err = attachBoxMessageImages(ctx, tx, p.AccountID, message.ID, request.Images); err != nil {
		return message, false, err
	}
	if err = tx.Commit(); err != nil {
		return message, false, err
	}
	values := []v1.BoxMessage{message}
	err = s.loadBoxMessageImages(ctx, p.AccountID, values)
	return values[0], false, err
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, s.loadBoxMessageImages(ctx, p.AccountID, values)
}

func (s *Store) UnansweredBoxMessages(ctx context.Context, p Principal, taskID string) ([]v1.BoxMessage, error) {
	if _, err := s.BoxTask(ctx, p, taskID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT m.id::text,m.task_id::text,COALESCE(m.user_id::text,''),m.direction,m.body,m.state,m.created_at,m.updated_at,COALESCE(m.chat_key,''),COALESCE(m.sender_box_id::text,''),COALESCE(m.parent_message_id::text,''),COALESCE(m.thread_id,m.id)::text
		FROM box_messages m
		WHERE m.account_id=$1 AND m.task_id=$2 AND m.direction='user' AND m.state IN ('delivered','read') AND m.submit
		AND NOT EXISTS (
			SELECT 1 FROM box_messages reply
			WHERE reply.account_id=m.account_id AND reply.idempotency_key='agent-reply:' || m.id::text
			  AND reply.state='delivered'
		)
		ORDER BY m.created_at,m.id`, p.AccountID, taskID)
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

func (s *Store) UpsertAgentBoxMessage(ctx context.Context, accountID, taskID, replyTo, text, state string) (bool, error) {
	if strings.TrimSpace(text) == "" || len(text) > 100_000 {
		return false, fmt.Errorf("agent reply must contain between 1 and 100000 bytes")
	}
	if state != "streaming" && state != "delivered" {
		return false, fmt.Errorf("agent reply state must be streaming or delivered")
	}
	result, err := s.DB.ExecContext(ctx, `WITH target AS (
		SELECT id,thread_id FROM box_messages WHERE account_id=$2 AND task_id=$3 AND id=$7
	), confirmed AS (
		UPDATE box_messages parent SET state='read',failure_reason=NULL,updated_at=now()
		FROM target WHERE parent.id=target.id AND parent.account_id=$2
		AND parent.direction IN ('user','box') AND parent.state<>'read' RETURNING parent.id
	)
	INSERT INTO box_messages(id,account_id,task_id,direction,body,submit,state,idempotency_key,parent_message_id,thread_id)
		SELECT $1,$2,$3,'agent',$4,false,$5,$6,target.id,COALESCE(target.thread_id,target.id)
		FROM target LEFT JOIN confirmed ON confirmed.id=target.id
		ON CONFLICT(account_id,idempotency_key) DO UPDATE
		SET body=EXCLUDED.body,state=EXCLUDED.state,failure_reason=NULL,updated_at=now()
		WHERE box_messages.direction='agent'
		  AND (box_messages.body IS DISTINCT FROM EXCLUDED.body OR box_messages.state IS DISTINCT FROM EXCLUDED.state)`,
		uuid(), accountID, taskID, text, state, "agent-reply:"+replyTo, replyTo)
	if err != nil {
		return false, err
	}
	changed, _ := result.RowsAffected()
	return changed == 1, nil
}

func (s *Store) AgentBoxMessage(ctx context.Context, accountID, replyTo string) (v1.BoxMessage, bool, error) {
	message, err := scanBoxMessage(s.DB.QueryRowContext(ctx, boxMessageSelect+" WHERE account_id=$1 AND direction='agent' AND idempotency_key=$2", accountID, "agent-reply:"+replyTo))
	if errors.Is(err, sql.ErrNoRows) {
		return message, false, nil
	}
	return message, err == nil, err
}

// AgentReplyWatchActive is false once the conversation or its box has left the
// state in which a reply can arrive. In particular, logical-box deletion
// cascades the task away; reply watchers must not keep polling a removed worker.
func (s *Store) AgentReplyWatchActive(ctx context.Context, accountID, taskID string) (bool, error) {
	var active bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM box_tasks t JOIN logical_boxes b ON b.id=t.logical_box_id AND b.account_id=t.account_id
		WHERE t.account_id=$1 AND t.id=$2 AND t.state='active' AND b.state='running'
	)`, accountID, taskID).Scan(&active)
	return active, err
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
	_, err := s.DB.ExecContext(ctx, `WITH message AS (
		UPDATE box_messages SET state=$3,failure_reason=NULLIF($4,''),updated_at=now()
		WHERE account_id=$1 AND id=$2 AND state<>'read'
		AND (state<>'delivered' OR $3 IN ('delivered','read')) RETURNING id,task_id,direction,submit
	) UPDATE box_tasks task SET agent_busy=true,agent_busy_updated_at=now(),agent_busy_message_id=message.id
	FROM message WHERE task.account_id=$1 AND task.id=message.task_id AND task.agent<>'shell'
	AND $3 IN ('delivered','read') AND message.submit AND message.direction IN ('user','box')
	AND NOT EXISTS (SELECT 1 FROM box_messages reply WHERE reply.account_id=$1
		AND reply.idempotency_key='agent-reply:' || message.id::text AND reply.state='delivered')`, accountID, id, state, failure)
	return err
}

func (s *Store) RecoverStaleBoxMessages(ctx context.Context, before time.Time) error {
	// A worker can publish its reply before the delivery call returns. Repair
	// messages left in-flight by a controller restart or an old request timeout.
	if _, err := s.DB.ExecContext(ctx, `UPDATE box_messages parent SET state='read',failure_reason=NULL,updated_at=now()
		WHERE parent.direction IN ('user','box') AND parent.state<>'read' AND EXISTS (
			SELECT 1 FROM box_messages reply WHERE reply.account_id=parent.account_id
			AND reply.idempotency_key='agent-reply:' || parent.id::text AND reply.state='delivered'
		)`); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE box_messages SET state='ambiguous',failure_reason='delivery was interrupted; inspect the terminal before retrying',updated_at=now() WHERE state='delivering' AND updated_at < $1", before)
	return err
}

func (s *Store) FirstQueuedTaskMessage(ctx context.Context, accountID, taskID string) (v1.BoxMessage, bool, bool, error) {
	var submit bool
	message, err := scanBoxMessageWithSubmit(s.DB.QueryRowContext(ctx, "SELECT "+boxMessageColumns+",submit FROM box_messages WHERE account_id=$1 AND task_id=$2 AND state='queued' ORDER BY created_at,id LIMIT 1", accountID, taskID), &submit)
	if errors.Is(err, sql.ErrNoRows) {
		return message, false, false, nil
	}
	return message, submit, err == nil, err
}

func scanBoxMessageWithSubmit(scanner interface{ Scan(...any) error }, submit *bool) (v1.BoxMessage, error) {
	var message v1.BoxMessage
	err := scanner.Scan(&message.ID, &message.TaskID, &message.UserID, &message.Direction, &message.Text, &message.State, &message.CreatedAt, &message.UpdatedAt, &message.ChatKey, &message.SenderBoxID, &message.ParentMessageID, &message.ThreadID, submit)
	decodeBoxMessageQuestion(&message)
	decodeBoxMessageCaptcha(&message)
	decodeBoxMessageMail(&message)
	decodeBoxMessageControl(&message)
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
	return s.activeBoxMessagesByState(ctx, "queued")
}

func (s *Store) AmbiguousActiveBoxMessages(ctx context.Context) ([]queuedBoxMessage, error) {
	return s.activeBoxMessagesByState(ctx, "ambiguous")
}

func (s *Store) activeBoxMessagesByState(ctx context.Context, state string) ([]queuedBoxMessage, error) {
	if state != "queued" && state != "ambiguous" {
		return nil, fmt.Errorf("unsupported message state")
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT m.account_id::text,m.id::text,m.task_id::text,m.submit FROM box_messages m JOIN box_tasks t ON t.id=m.task_id AND t.account_id=m.account_id WHERE m.state='"+state+"' AND t.state='active' ORDER BY m.created_at,m.id")
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
