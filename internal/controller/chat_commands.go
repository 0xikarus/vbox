package controller

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type chatCommand struct {
	Name      string    `json:"name"`
	Prompt    string    `json:"prompt"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func validChatCommandName(name string) bool {
	if len(name) < 1 || len(name) > 40 {
		return false
	}
	for i, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || i > 0 && (r == '-' || r == '_') {
			continue
		}
		return false
	}
	return true
}

func (s *Store) listChatCommands(ctx context.Context, accountID string) ([]chatCommand, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT name,prompt,created_at,updated_at FROM chat_commands WHERE account_id=$1 ORDER BY name`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []chatCommand{}
	for rows.Next() {
		var value chatCommand
		if err := rows.Scan(&value.Name, &value.Prompt, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Server) chatCommandsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	values, err := s.Store.listChatCommands(r.Context(), p.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not list chat commands"))
		return
	}
	writeJSON(w, http.StatusOK, values)
}

func (s *Server) chatCommandHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	name := r.PathValue("name")
	if !validChatCommandName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("command names use 1–40 lowercase letters, digits, hyphens or underscores and start with a letter or digit"))
		return
	}
	if r.Method == http.MethodDelete {
		result, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM chat_commands WHERE account_id=$1 AND name=$2`, p.AccountID, name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("could not delete chat command"))
			return
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			writeError(w, http.StatusNotFound, fmt.Errorf("chat command not found"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var request struct {
		Prompt string `json:"prompt"`
	}
	if err := decodeJSON(r, &request); err != nil || strings.TrimSpace(request.Prompt) == "" || len(request.Prompt) > 10_000 || strings.ContainsRune(request.Prompt, '\x00') {
		writeError(w, http.StatusBadRequest, fmt.Errorf("command prompt must contain 1–10000 bytes of text"))
		return
	}
	var value chatCommand
	err := s.Store.DB.QueryRowContext(r.Context(), `INSERT INTO chat_commands(account_id,name,prompt) VALUES($1,$2,$3)
		ON CONFLICT(account_id,name) DO UPDATE SET prompt=excluded.prompt,updated_at=now()
		RETURNING name,prompt,created_at,updated_at`, p.AccountID, name, request.Prompt).
		Scan(&value.Name, &value.Prompt, &value.CreatedAt, &value.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not save chat command"))
		return
	}
	writeJSON(w, http.StatusOK, value)
}
