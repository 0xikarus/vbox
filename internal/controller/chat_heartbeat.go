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

type chatHeartbeatRequest struct {
	Count           int    `json:"count"`
	IntervalMinutes int    `json:"intervalMinutes"`
	Message         string `json:"message"`
}

func (r chatHeartbeatRequest) validate() error {
	if r.Count < 1 || r.Count > 1000 {
		return fmt.Errorf("count must be between 1 and 1000")
	}
	if r.IntervalMinutes < 1 || r.IntervalMinutes > 1440 {
		return fmt.Errorf("intervalMinutes must be between 1 and 1440")
	}
	if strings.TrimSpace(r.Message) == "" || len(r.Message) > 100000 {
		return fmt.Errorf("heartbeat message must contain between 1 and 100000 bytes")
	}
	// These prefixes are controller commands, not ordinary agent messages.
	if _, silent := silentMessage(r.Message); silent {
		return fmt.Errorf("heartbeat message cannot be a /silent note")
	}
	return nil
}

type chatHeartbeat struct {
	ID              string    `json:"id"`
	Count           int       `json:"count"`
	IntervalMinutes int       `json:"intervalMinutes"`
	Message         string    `json:"message"`
	Remaining       int       `json:"remaining"`
	NextAt          time.Time `json:"nextAt"`
	Paused          bool      `json:"paused"`
	Active          bool      `json:"active"`
}

const heartbeatColumns = `h.id::text,h.count,h.interval_minutes,h.message,h.remaining,h.next_at,(b.state<>'running'),(h.stopped_at IS NULL AND h.remaining>0)`

func scanChatHeartbeat(row interface{ Scan(...any) error }) (h chatHeartbeat, err error) {
	err = row.Scan(&h.ID, &h.Count, &h.IntervalMinutes, &h.Message, &h.Remaining, &h.NextAt, &h.Paused, &h.Active)
	return
}

func (s *Store) startChatHeartbeat(ctx context.Context, p Principal, boxID, key string, r chatHeartbeatRequest) (chatHeartbeat, error) {
	var empty chatHeartbeat
	if err := r.validate(); err != nil {
		return empty, err
	}
	if key == "" || len(key) > 128 {
		return empty, fmt.Errorf("Idempotency-Key is required (up to 128 characters)")
	}
	if p.Role != "owner" {
		return empty, fmt.Errorf("owner access required")
	}
	box, err := s.LogicalBox(ctx, p, boxID)
	if err != nil {
		return empty, err
	}
	if box.State == v1.LogicalBoxDeleting {
		return empty, fmt.Errorf("box is being deleted")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	// Serialize starts even when this box has never had a heartbeat.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1731))`, p.AccountID+":"+box.ID); err != nil {
		return empty, err
	}
	previous, err := scanChatHeartbeat(tx.QueryRowContext(ctx, `SELECT `+heartbeatColumns+` FROM chat_heartbeats h JOIN logical_boxes b ON b.id=h.box_id WHERE h.account_id=$1 AND h.box_id=$2 AND h.idempotency_key=$3`, p.AccountID, box.ID, key))
	if err == nil {
		if previous.Count != r.Count || previous.IntervalMinutes != r.IntervalMinutes || previous.Message != r.Message {
			return empty, fmt.Errorf("idempotency key belongs to different heartbeat parameters")
		}
		return previous, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE chat_heartbeats SET stopped_at=now() WHERE account_id=$1 AND box_id=$2 AND stopped_at IS NULL AND remaining>0`, p.AccountID, box.ID); err != nil {
		return empty, err
	}
	id := uuid()
	if _, err = tx.ExecContext(ctx, `INSERT INTO chat_heartbeats(id,account_id,box_id,user_id,count,interval_minutes,message,remaining,next_at,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,$7,$5,now()+$6::integer*interval '1 minute',$8)`, id, p.AccountID, box.ID, p.UserID, r.Count, r.IntervalMinutes, r.Message, key); err != nil {
		return empty, err
	}
	h, err := scanChatHeartbeat(tx.QueryRowContext(ctx, `SELECT `+heartbeatColumns+` FROM chat_heartbeats h JOIN logical_boxes b ON b.id=h.box_id WHERE h.id=$1`, id))
	if err != nil {
		return empty, err
	}
	return h, tx.Commit()
}

func (s *Server) activeToolsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT `+heartbeatColumns+` FROM chat_heartbeats h JOIN logical_boxes b ON b.id=h.box_id WHERE h.account_id=$1 AND h.box_id=$2 AND h.stopped_at IS NULL AND h.remaining>0 ORDER BY h.created_at`, p.AccountID, box.ID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer rows.Close()
	list := []chatHeartbeat{}
	for rows.Next() {
		h, err := scanChatHeartbeat(rows)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		list = append(list, h)
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"heartbeats": list})
}

func (s *Server) startChatHeartbeatHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var body chatHeartbeatRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, 400, err)
		return
	}
	h, err := s.Store.startChatHeartbeat(r.Context(), p, r.PathValue("id"), r.Header.Get("Idempotency-Key"), body)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 201, h)
}

func (s *Server) stopChatHeartbeatHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	// Stops the selected timer only: an old panel cannot cancel its replacement.
	result, err := s.Store.DB.ExecContext(r.Context(), `UPDATE chat_heartbeats SET stopped_at=COALESCE(stopped_at,now()) WHERE account_id=$1 AND box_id=$2 AND id::text=$3`, p.AccountID, box.ID, r.PathValue("heartbeat"))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		writeError(w, 404, fmt.Errorf("heartbeat not found"))
		return
	}
	w.WriteHeader(204)
}

type heartbeatDelivery func(context.Context, Principal, string, string, v1.DirectBoxMessageRequest) (v1.DirectBoxMessageResponse, error)

// A locked timer plus the ordinary chat route's stable idempotency key survives
// a controller crash between message persistence and advancing the timer.
// Stop/replacement waits for an in-flight tick; already queued chat stays queued.
func (s *Store) advanceChatHeartbeat(ctx context.Context, id string, deliver heartbeatDelivery) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var p Principal
	var boxID string
	var h chatHeartbeat
	err = tx.QueryRowContext(ctx, `SELECT h.id::text,h.account_id::text,h.box_id::text,h.user_id::text,h.count,h.interval_minutes,h.message,h.remaining FROM chat_heartbeats h JOIN logical_boxes b ON b.id=h.box_id AND b.account_id=h.account_id JOIN users u ON u.id=h.user_id AND u.account_id=h.account_id WHERE h.id=$1 AND h.stopped_at IS NULL AND h.remaining>0 AND h.next_at<=now() AND b.state='running' AND u.role='owner' AND u.disabled_at IS NULL FOR UPDATE OF h SKIP LOCKED`, id).Scan(&h.ID, &p.AccountID, &boxID, &p.UserID, &h.Count, &h.IntervalMinutes, &h.Message, &h.Remaining)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	p.Role = "owner"
	key := fmt.Sprintf("chat-heartbeat:%s:%d", h.ID, h.Count-h.Remaining+1)
	if _, err = deliver(ctx, p, boxID, key, v1.DirectBoxMessageRequest{Text: h.Message}); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE chat_heartbeats SET remaining=remaining-1,next_at=now()+interval_minutes*interval '1 minute' WHERE id=$1`, h.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) reconcileChatHeartbeats(ctx context.Context) error {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT h.id::text FROM chat_heartbeats h JOIN logical_boxes b ON b.id=h.box_id JOIN users u ON u.id=h.user_id WHERE h.stopped_at IS NULL AND h.remaining>0 AND h.next_at<=now() AND b.state='running' AND u.role='owner' AND u.disabled_at IS NULL ORDER BY h.next_at LIMIT 100`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		tickCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.Store.advanceChatHeartbeat(tickCtx, id, s.routeBoxMessage)
		cancel()
		if err != nil {
			s.Logger.Warn("chat heartbeat delivery deferred", "heartbeat", id, "error", err)
		}
	}
	return nil
}

func (s *Server) startChatHeartbeatReconciler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			if err := s.reconcileChatHeartbeats(ctx); err != nil {
				s.Logger.Warn("chat heartbeat reconciliation failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
