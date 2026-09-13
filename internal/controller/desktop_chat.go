package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

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
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT id,task_id,user_id,direction,body,state,created_at,updated_at FROM (
 SELECT m.id::text,m.task_id::text,COALESCE(m.user_id::text,''),m.direction,m.body,m.state,m.created_at,m.updated_at FROM box_messages m JOIN box_tasks t ON t.id=m.task_id WHERE m.account_id=$1 AND t.logical_box_id=$2
 UNION ALL SELECT id::text,''::text,user_id::text,'user',body,'silent',created_at,created_at FROM box_notes WHERE account_id=$1 AND box_id=$2
 ) AS history(id,task_id,user_id,direction,body,state,created_at,updated_at) ORDER BY created_at DESC LIMIT 500`, p.AccountID, box.ID)
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
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, values)
}
