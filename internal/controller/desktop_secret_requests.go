package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/secrets"
)

type privateSecretRequestKey struct{}

func (s *Server) requestDesktopSecret(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := context.WithValue(r.Context(), privateSecretRequestKey{}, true)
	s.useDesktopSecret(w, r.WithContext(ctx), p, true, secrets.PasswordPolicy{})
}

func (s *Server) desktopSecretRequests(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	if r.Method == "GET" {
		rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT secret_key,origin,status,created_at FROM desktop_secret_requests WHERE account_id=$1 AND box_id=$2 ORDER BY created_at`, p.AccountID, box.ID)
		if err != nil {
			writeError(w, 500, fmt.Errorf("requests unavailable"))
			return
		}
		defer rows.Close()
		values := []map[string]any{}
		for rows.Next() {
			var key, origin, status string
			var created time.Time
			if rows.Scan(&key, &origin, &status, &created) != nil {
				writeError(w, 500, fmt.Errorf("requests unavailable"))
				return
			}
			values = append(values, map[string]any{"key": key, "origin": origin, "status": status, "createdAt": created})
		}
		if rows.Err() != nil {
			writeError(w, 500, fmt.Errorf("requests unavailable"))
			return
		}
		writeJSON(w, 200, values)
		return
	}
	key := r.PathValue("key")
	if !loginProfileName.MatchString(key) {
		writeError(w, 400, fmt.Errorf("invalid reference"))
		return
	}
	var request struct {
		Value  string `json:"value"`
		Cancel bool   `json:"cancel"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || (!request.Cancel && (request.Value == "" || len(request.Value) > 4096)) || (request.Cancel && request.Value != "") {
		writeError(w, 400, fmt.Errorf("supply a private value or cancel the request"))
		return
	}
	plain := []byte(request.Value)
	request.Value = ""
	defer clear(plain)
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 503, fmt.Errorf("request unavailable"))
		return
	}
	defer tx.Rollback()
	var locked string
	if tx.QueryRowContext(r.Context(), `SELECT id::text FROM logical_boxes WHERE id=$1 AND account_id=$2 FOR UPDATE`, box.ID, p.AccountID).Scan(&locked) != nil {
		writeError(w, 409, fmt.Errorf("box unavailable"))
		return
	}
	var origin, status string
	err = tx.QueryRowContext(r.Context(), `SELECT origin,status FROM desktop_secret_requests WHERE account_id=$1 AND box_id=$2 AND secret_key=$3 FOR UPDATE`, p.AccountID, box.ID, key).Scan(&origin, &status)
	if err != nil {
		writeError(w, 404, fmt.Errorf("request unavailable"))
		return
	}
	if status != "pending" {
		writeJSON(w, 200, map[string]string{"key": key, "status": status})
		return
	}
	status = "cancelled"
	if !request.Cancel {
		_, created, saveErr := s.Store.ensureDesktopSecret(r.Context(), tx, p, box.ID, key, origin, plain, secrets.PasswordPolicy{})
		if saveErr != nil || !created {
			writeError(w, 409, fmt.Errorf("credential reference conflicts or could not be saved"))
			return
		}
		status = "fulfilled"
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE desktop_secret_requests SET status=$4 WHERE account_id=$1 AND box_id=$2 AND secret_key=$3`, p.AccountID, box.ID, key, status); err != nil {
		writeError(w, 500, fmt.Errorf("request could not be updated"))
		return
	}
	if tx.Commit() != nil {
		writeError(w, 409, fmt.Errorf("request outcome uncertain; reload requests"))
		return
	}
	// Notify the same box's current task using a reference only. A sleeping box is
	// never woken by credential submission or cancellation.
	if box.State == v1.LogicalBoxRunning {
		if tasks, e := s.Store.ListBoxTasks(r.Context(), p, box.ID); e == nil {
			if task := reusableBoxTask(tasks, box.State, box.DefaultAgent, ""); task != nil && task.State == "active" {
				text := "Private credential request " + key + " was " + status + "."
				if status == "fulfilled" {
					text += " Use type_secret with that reference; no password is included in chat."
				}
				if message, _, e := s.Store.CreateBoxMessage(r.Context(), p, task.ID, "secret-request:"+box.ID+":"+key+":"+status, v1.SendBoxMessageRequest{Text: text}); e == nil && message.State == "queued" {
					_ = s.deliverBoxMessage(r.Context(), p, *task, message, true)
				}
			}
		}
	}
	writeJSON(w, 200, map[string]string{"key": key, "status": status})
}
