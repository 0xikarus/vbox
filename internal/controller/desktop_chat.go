package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

var historyMessageID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func silentMessage(text string) (string, bool) {
	if text == "/silent" {
		return "", true
	}
	if strings.HasPrefix(text, "/silent ") || strings.HasPrefix(text, "/silent\n") {
		return strings.TrimSpace(text[len("/silent"):]), true
	}
	return text, false
}

func (s *Store) boxNote(ctx context.Context, p Principal, box, key string) (v1.BoxMessage, bool, error) {
	var note v1.BoxMessage
	err := s.DB.QueryRowContext(ctx, `SELECT id::text,user_id::text,body,created_at FROM box_notes WHERE account_id=$1 AND box_id=$2 AND idempotency_key=$3`, p.AccountID, box, key).Scan(&note.ID, &note.UserID, &note.Text, &note.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return note, false, nil
	}
	note.Direction = "user"
	note.State = "silent"
	note.UpdatedAt = note.CreatedAt
	return note, err == nil, err
}

func (s *Server) boxMessageHistory(w http.ResponseWriter, r *http.Request, p Principal) {
	limit := 500
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, 400, fmt.Errorf("limit must be between 1 and 500"))
			return
		}
		limit = parsed
	}
	var before, beforeID any
	if raw := r.URL.Query().Get("before"); raw != "" || r.URL.Query().Get("beforeId") != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		id := r.URL.Query().Get("beforeId")
		if err != nil || !historyMessageID.MatchString(id) {
			writeError(w, 400, fmt.Errorf("before and beforeId must identify a message"))
			return
		}
		before, beforeID = parsed, id
	}
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	drainCtx, cancelDrain := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	s.drainBoxChat(drainCtx, p, box)
	cancelDrain()
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT id,task_id,user_id,direction,body,state,created_at,updated_at,chat_key,sender_box_id FROM (
 SELECT m.id::text,m.task_id::text,COALESCE(m.user_id::text,''),m.direction,m.body,m.state,m.created_at,m.updated_at,COALESCE(m.chat_key,''),COALESCE(m.sender_box_id::text,'') FROM box_messages m JOIN box_tasks t ON t.id=m.task_id WHERE m.account_id=$1 AND t.logical_box_id=$2
 UNION ALL SELECT id::text,''::text,user_id::text,'user',body,'silent',created_at,created_at,''::text,''::text FROM box_notes WHERE account_id=$1 AND box_id=$2
 ) AS history(id,task_id,user_id,direction,body,state,created_at,updated_at,chat_key,sender_box_id)
 WHERE ($3::timestamptz IS NULL OR (created_at,id)<($3::timestamptz,$4::text))
 ORDER BY created_at DESC,id DESC LIMIT $5`, p.AccountID, box.ID, before, beforeID, limit)
	if err != nil {
		writeError(w, 500, fmt.Errorf("message history unavailable"))
		return
	}
	defer rows.Close()
	values := []v1.BoxMessage{}
	for rows.Next() {
		value, err := scanBoxMessage(rows)
		if err != nil {
			writeError(w, 500, fmt.Errorf("message history unavailable"))
			return
		}
		values = append(values, value)
	}
	if rows.Err() != nil {
		writeError(w, 500, fmt.Errorf("message history unavailable"))
		return
	}
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
	if err := s.Store.loadBoxMessageImages(r.Context(), p.AccountID, values); err != nil {
		writeError(w, 500, fmt.Errorf("message images unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, values)
}

// clearBoxContextHandler starts a fresh context inside the active chat task's
// existing tmux session. Chat history remains an audit trail; only the agent's
// in-memory conversation is reset.
func (s *Server) clearBoxContextHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("Idempotency-Key is required"))
		return
	}
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("box unavailable"))
		return
	}
	if box.State != v1.LogicalBoxRunning {
		writeError(w, http.StatusConflict, fmt.Errorf("logical box is %s, not running", box.State))
		return
	}
	if box.DefaultAgent != "codex" && box.DefaultAgent != "claude" && box.DefaultAgent != "opencode" {
		writeError(w, http.StatusConflict, fmt.Errorf("the box does not use an agent context"))
		return
	}
	tasks, err := s.Store.ListBoxTasks(r.Context(), p, box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("chat task unavailable"))
		return
	}
	task := reusableBoxTask(tasks, box.State, box.DefaultAgent, "")
	if task == nil || task.State != "active" {
		writeError(w, http.StatusConflict, fmt.Errorf("no active %s chat context to clear", box.DefaultAgent))
		return
	}
	assignment, err := s.Store.assignment(r.Context(), p.AccountID, box.ID)
	if err != nil {
		writeError(w, http.StatusConflict, fmt.Errorf("box assignment unavailable"))
		return
	}
	prov, err := s.provider(r.Context(), p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("worker unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resetID := terminalInputMessageID(p.AccountID, box.ID, task.Session, key)
	result, execErr := prov.Exec(ctx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "chat-reset", task.Session, task.Agent, resetID}, provider.ExecOptions{})
	if execErr != nil || result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" && execErr != nil {
			detail = execErr.Error()
		}
		writeError(w, http.StatusConflict, fmt.Errorf("could not clear agent context: %s", detail))
		return
	}
	// The reset has completed in the watched harness. Record the visible audit
	// marker even if the browser disconnected while waiting for the worker.
	recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer recordCancel()
	if err := s.recordContextClear(recordCtx, p.AccountID, task.ID, task.Agent, resetID); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("agent context cleared, but chat marker could not be saved"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"agent": task.Agent, "session": task.Session, "taskId": task.ID})
}

func (s *Server) recordContextClear(ctx context.Context, accountID, taskID, agent, resetID string) error {
	return s.Store.AppendSystemBoxMessage(ctx, accountID, taskID, "context cleared · "+agent+" is ready", "context-clear:"+resetID)
}
