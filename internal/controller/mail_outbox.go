package controller

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func validateMailDraft(to []string, subject, text string) ([]string, error) {
	recipients, err := normalizeMailRecipients(to)
	if err != nil {
		return nil, err
	}
	subject = strings.TrimSpace(subject)
	if subject == "" || len(subject) > 200 || strings.ContainsAny(subject, "\r\n") {
		return nil, fmt.Errorf("subject must contain 1–200 characters on one line")
	}
	if strings.TrimSpace(text) == "" || len(text) > 50000 {
		return nil, fmt.Errorf("text must contain 1–50000 bytes")
	}
	return recipients, nil
}

func (s *Server) ownerMailOutboxItem(w http.ResponseWriter, r *http.Request, p Principal) {
	box, ok := s.ownerMailBox(w, r, p)
	if !ok {
		return
	}
	id := r.PathValue("oid")
	if !mailUUIDPattern.MatchString(id) {
		writeError(w, 404, fmt.Errorf("outbox item unavailable"))
		return
	}
	if r.Method == http.MethodGet {
		item, err := s.Store.mailOutboxItem(r.Context(), p.AccountID, box.ID, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, fmt.Errorf("outbox item unavailable"))
			return
		}
		if err != nil {
			writeError(w, 500, fmt.Errorf("outbox item unavailable"))
			return
		}
		writeJSON(w, 200, item)
		return
	}
	var request struct {
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
		Version int      `json:"version"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	recipients, err := validateMailDraft(request.To, request.Subject, request.Text)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if request.Version < 1 {
		writeError(w, 400, fmt.Errorf("outbox version is required"))
		return
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, fmt.Errorf("outbox unavailable"))
		return
	}
	defer tx.Rollback()
	var oldStatus string
	var oldVersion int
	err = tx.QueryRowContext(r.Context(), `SELECT status,version FROM mail_outbox WHERE account_id=$1 AND box_id=$2 AND id=$3 FOR UPDATE`, p.AccountID, box.ID, id).Scan(&oldStatus, &oldVersion)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, fmt.Errorf("outbox item unavailable"))
		return
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("outbox unavailable"))
		return
	}
	if oldStatus != "pending_approval" || oldVersion != request.Version {
		writeError(w, 409, fmt.Errorf("outbox draft changed or was already decided"))
		return
	}
	raw, _ := json.Marshal(recipients)
	if _, err := tx.ExecContext(r.Context(), `UPDATE mail_outbox SET recipient_list=$4::jsonb,subject=$5,reviewed_text=$6,version=version+1,updated_at=now() WHERE account_id=$1 AND box_id=$2 AND id=$3`, p.AccountID, box.ID, id, string(raw), strings.TrimSpace(request.Subject), request.Text); err != nil {
		writeError(w, 500, fmt.Errorf("outbox edit failed"))
		return
	}
	detail, _ := json.Marshal(map[string]any{"version": oldVersion + 1, "to": recipients, "subject": strings.TrimSpace(request.Subject), "text": request.Text})
	if err := appendMailEvent(r.Context(), tx, p.AccountID, box.ID, "", id, "edit", "owner:"+p.UserID, detail); err != nil {
		writeError(w, 500, fmt.Errorf("outbox audit failed"))
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, 500, fmt.Errorf("outbox edit failed"))
		return
	}
	item, err := s.Store.mailOutboxItem(r.Context(), p.AccountID, box.ID, id)
	if err != nil {
		writeError(w, 500, fmt.Errorf("outbox unavailable"))
		return
	}
	writeJSON(w, 200, item)
}

func (s *Store) mailOutboxItem(ctx context.Context, accountID, boxID, id string) (mailOutboxRow, error) {
	return scanMailOutbox(s.DB.QueryRowContext(ctx, `SELECT id::text,recipient_list,subject,reviewed_text,status,reason,version,created_at,decided_at,sent_at FROM mail_outbox WHERE account_id=$1 AND box_id=$2 AND id=$3`, accountID, boxID, id))
}

func appendMailEvent(ctx context.Context, tx *sql.Tx, accountID, boxID, messageID, outboxID, kind, actor string, detail []byte) error {
	if len(detail) == 0 {
		detail = []byte(`{}`)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO mail_events(id,account_id,box_id,message_id,outbox_id,kind,actor,detail) VALUES($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,$7,$8::jsonb)`, uuid(), accountID, boxID, messageID, outboxID, kind, actor, string(detail))
	return err
}

func (s *Server) ownerMailApprove(w http.ResponseWriter, r *http.Request, p Principal) {
	box, ok := s.ownerMailBox(w, r, p)
	if !ok {
		return
	}
	id := r.PathValue("oid")
	if !mailUUIDPattern.MatchString(id) {
		writeError(w, 404, fmt.Errorf("outbox item unavailable"))
		return
	}
	if mailDomain() == "" || os.Getenv("VMBOX_RESEND_API_KEY") == "" {
		writeError(w, 503, fmt.Errorf("mail sending is disabled"))
		return
	}
	var request struct {
		Version int `json:"version"`
	}
	if err := decodeJSON(r, &request); err != nil || request.Version < 1 {
		writeError(w, 400, fmt.Errorf("reviewed outbox version is required"))
		return
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, fmt.Errorf("outbox unavailable"))
		return
	}
	defer tx.Rollback()
	var status, address, subject, text string
	var version int
	var toRaw []byte
	err = tx.QueryRowContext(r.Context(), `SELECT o.status,o.version,COALESCE(m.address,''),o.recipient_list,o.subject,o.reviewed_text FROM mail_outbox o JOIN box_mail_settings m ON m.box_id=o.box_id AND m.account_id=o.account_id WHERE o.account_id=$1 AND o.box_id=$2 AND o.id=$3 FOR UPDATE OF o`, p.AccountID, box.ID, id).Scan(&status, &version, &address, &toRaw, &subject, &text)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, fmt.Errorf("outbox item unavailable"))
		return
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("outbox unavailable"))
		return
	}
	if status != "pending_approval" || version != request.Version {
		writeError(w, 409, fmt.Errorf("outbox draft changed or was already decided"))
		return
	}
	if address == "" {
		writeError(w, 409, fmt.Errorf("box mail address unavailable"))
		return
	}
	var to []string
	if json.Unmarshal(toRaw, &to) != nil {
		writeError(w, 500, fmt.Errorf("outbox recipient list unavailable"))
		return
	}
	if _, err := validateMailDraft(to, subject, text); err != nil {
		writeError(w, 409, fmt.Errorf("outbox draft invalid"))
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE mail_outbox SET status='sending',decided_at=now(),updated_at=now() WHERE account_id=$1 AND box_id=$2 AND id=$3`, p.AccountID, box.ID, id); err != nil {
		writeError(w, 500, fmt.Errorf("outbox approval failed"))
		return
	}
	detail, _ := json.Marshal(map[string]any{"version": version})
	if err := appendMailEvent(r.Context(), tx, p.AccountID, box.ID, "", id, "approve", "owner:"+p.UserID, detail); err != nil {
		writeError(w, 500, fmt.Errorf("outbox audit failed"))
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, 500, fmt.Errorf("outbox approval failed"))
		return
	}
	providerID, sendErr := s.sendApprovedMail(r.Context(), id, address, to, subject, text)
	nextStatus, reason := "sent", ""
	if sendErr != nil {
		nextStatus, reason = "failed", "Send provider unavailable or rejected the message."
	}
	_, err = s.Store.DB.ExecContext(r.Context(), `UPDATE mail_outbox SET status=$4,reason=$5,provider_id=$6,sent_at=CASE WHEN $4='sent' THEN now() ELSE NULL END,updated_at=now() WHERE account_id=$1 AND box_id=$2 AND id=$3 AND status='sending'`, p.AccountID, box.ID, id, nextStatus, reason, providerID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("send outcome could not be recorded"))
		return
	}
	s.notifyBoxMailStatus(p.AccountID, box.ID, id, to, subject, nextStatus, reason)
	item, err := s.Store.mailOutboxItem(r.Context(), p.AccountID, box.ID, id)
	if err != nil {
		writeError(w, 500, fmt.Errorf("outbox unavailable"))
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) sendApprovedMail(ctx context.Context, id, from string, to []string, subject, text string) (string, error) {
	endpoint := s.ResendURL
	if endpoint == "" {
		endpoint = "https://api.resend.com/emails"
	}
	payload, _ := json.Marshal(map[string]any{"from": from, "to": to, "subject": subject, "text": text})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+os.Getenv("VMBOX_RESEND_API_KEY"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", id)
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("send provider unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("send provider rejected request")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("send provider response unreadable")
	}
	var result struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &result) != nil || result.ID == "" {
		return "", fmt.Errorf("send provider response invalid")
	}
	return result.ID, nil
}

func (s *Server) ownerMailReject(w http.ResponseWriter, r *http.Request, p Principal) {
	box, ok := s.ownerMailBox(w, r, p)
	if !ok {
		return
	}
	id := r.PathValue("oid")
	if !mailUUIDPattern.MatchString(id) {
		writeError(w, 404, fmt.Errorf("outbox item unavailable"))
		return
	}
	var request struct {
		Reason  string `json:"reason"`
		Version int    `json:"version"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 || request.Version < 1 {
		writeError(w, 400, fmt.Errorf("reason and reviewed version are required"))
		return
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, fmt.Errorf("outbox unavailable"))
		return
	}
	defer tx.Rollback()
	var status, subject string
	var version int
	var toRaw []byte
	err = tx.QueryRowContext(r.Context(), `SELECT status,version,recipient_list,subject FROM mail_outbox WHERE account_id=$1 AND box_id=$2 AND id=$3 FOR UPDATE`, p.AccountID, box.ID, id).Scan(&status, &version, &toRaw, &subject)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, fmt.Errorf("outbox item unavailable"))
		return
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("outbox unavailable"))
		return
	}
	if status != "pending_approval" || version != request.Version {
		writeError(w, 409, fmt.Errorf("outbox draft changed or was already decided"))
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE mail_outbox SET status='rejected',reason=$4,decided_at=now(),updated_at=now() WHERE account_id=$1 AND box_id=$2 AND id=$3`, p.AccountID, box.ID, id, request.Reason); err != nil {
		writeError(w, 500, fmt.Errorf("outbox rejection failed"))
		return
	}
	detail, _ := json.Marshal(map[string]any{"version": version, "reason": request.Reason})
	if err := appendMailEvent(r.Context(), tx, p.AccountID, box.ID, "", id, "reject", "owner:"+p.UserID, detail); err != nil {
		writeError(w, 500, fmt.Errorf("outbox audit failed"))
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, 500, fmt.Errorf("outbox rejection failed"))
		return
	}
	var to []string
	_ = json.Unmarshal(toRaw, &to)
	s.notifyBoxMailStatus(p.AccountID, box.ID, id, to, subject, "rejected", request.Reason)
	item, err := s.Store.mailOutboxItem(r.Context(), p.AccountID, box.ID, id)
	if err != nil {
		writeError(w, 500, fmt.Errorf("outbox unavailable"))
		return
	}
	writeJSON(w, 200, item)
}

// notifyBoxMailStatus is implemented by the chat batch dispatcher below.
func (s *Server) notifyBoxMailStatus(accountID, boxID, outboxID string, to []string, subject, status, reason string) {
}
