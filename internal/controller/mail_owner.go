package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Server) ownerMailBox(w http.ResponseWriter, r *http.Request, p Principal) (v1.LogicalBox, bool) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("box unavailable"))
		return box, false
	}
	return box, true
}

func (s *Store) loadMailSettings(ctx context.Context, accountID, boxID string) (mailSettings, error) {
	value := mailSettings{}
	err := s.DB.QueryRowContext(ctx, `SELECT enabled,COALESCE(address,''),subscribed,sender_filter,subject_filter FROM box_mail_settings WHERE account_id=$1 AND box_id=$2`, accountID, boxID).Scan(&value.Enabled, &value.Address, &value.Subscribed, &value.Filters.Sender, &value.Filters.Subject)
	if errors.Is(err, sql.ErrNoRows) {
		return value, nil
	}
	return value, err
}

func (s *Store) mailSettingsCounts(ctx context.Context, accountID, boxID string, value *mailSettings) error {
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail_messages WHERE account_id=$1 AND box_id=$2 AND read_at IS NULL AND NOT quarantined AND expires_at>now()`, accountID, boxID).Scan(&value.Unread); err != nil {
		return err
	}
	return s.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail_outbox WHERE account_id=$1 AND box_id=$2 AND status='pending_approval'`, accountID, boxID).Scan(&value.Pending)
}

func mailBoxSlug(name string) string {
	var result strings.Builder
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			result.WriteRune(r)
		} else if result.Len() > 0 && !strings.HasSuffix(result.String(), "-") {
			result.WriteByte('-')
		}
		if result.Len() >= 40 {
			break
		}
	}
	slug := strings.Trim(result.String(), "-")
	if slug == "" {
		return "box"
	}
	return slug
}

func (s *Store) saveMailSettings(ctx context.Context, accountID string, box v1.LogicalBox, value mailSettings) (mailSettings, error) {
	domain := mailDomain()
	if domain == "" {
		return value, fmt.Errorf("mail is disabled")
	}
	for attempts := 0; attempts < 6; attempts++ {
		address := value.Address
		if value.Enabled && address == "" {
			address = mailBoxSlug(box.Name) + "-" + strings.ReplaceAll(uuid(), "-", "")[:4] + "@" + domain
		}
		_, err := s.DB.ExecContext(ctx, `INSERT INTO box_mail_settings(account_id,box_id,enabled,address,subscribed,sender_filter,subject_filter) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7)
   ON CONFLICT(box_id) DO UPDATE SET enabled=excluded.enabled,address=COALESCE(box_mail_settings.address,excluded.address),subscribed=excluded.subscribed,sender_filter=excluded.sender_filter,subject_filter=excluded.subject_filter,updated_at=now()`, accountID, box.ID, value.Enabled, address, value.Subscribed, value.Filters.Sender, value.Filters.Subject)
		if err == nil {
			return s.loadMailSettings(ctx, accountID, box.ID)
		}
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23505" || value.Address != "" {
			return value, err
		}
	}
	return value, fmt.Errorf("could not allocate a unique mail address")
}

func (s *Server) ownerMailSettings(w http.ResponseWriter, r *http.Request, p Principal) {
	box, ok := s.ownerMailBox(w, r, p)
	if !ok {
		return
	}
	current, err := s.Store.loadMailSettings(r.Context(), p.AccountID, box.ID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail settings unavailable"))
		return
	}
	if r.Method == http.MethodPut {
		if mailDomain() == "" {
			writeError(w, http.StatusServiceUnavailable, fmt.Errorf("mail is disabled"))
			return
		}
		var request struct {
			Enabled    *bool `json:"enabled"`
			Subscribed *bool `json:"subscribed"`
			Filters    *struct {
				Sender  *string `json:"sender"`
				Subject *string `json:"subject"`
			} `json:"filters"`
		}
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, 400, err)
			return
		}
		if request.Enabled != nil {
			current.Enabled = *request.Enabled
		}
		if request.Subscribed != nil {
			current.Subscribed = *request.Subscribed
		}
		if request.Filters != nil {
			if request.Filters.Sender != nil {
				current.Filters.Sender = strings.TrimSpace(*request.Filters.Sender)
			}
			if request.Filters.Subject != nil {
				current.Filters.Subject = strings.TrimSpace(*request.Filters.Subject)
			}
		}
		if len(current.Filters.Sender) > 254 || len(current.Filters.Subject) > 120 || strings.ContainsAny(current.Filters.Sender+current.Filters.Subject, "\r\n") {
			writeError(w, 400, fmt.Errorf("mail filters are invalid"))
			return
		}
		if !current.Enabled {
			current.Subscribed = false
		}
		current, err = s.Store.saveMailSettings(r.Context(), p.AccountID, box, current)
		if err != nil {
			writeError(w, 500, fmt.Errorf("mail settings could not be saved"))
			return
		}
	}
	if mailDomain() == "" {
		current.Enabled = false
		current.Subscribed = false
	}
	if err := s.Store.mailSettingsCounts(r.Context(), p.AccountID, box.ID, &current); err != nil {
		writeError(w, 500, fmt.Errorf("mail counts unavailable"))
		return
	}
	writeJSON(w, 200, current)
}

const mailMessageFields = `id::text,header_from,from_name,subject,preview,received_at,read_at IS NULL,has_attachments,quarantined,spf,dkim`

func scanMailMessage(scanner interface{ Scan(...any) error }) (mailMessageRow, error) {
	var value mailMessageRow
	err := scanner.Scan(&value.ID, &value.From, &value.FromName, &value.Subject, &value.Preview, &value.ReceivedAt, &value.Unread, &value.HasAttachments, &value.Quarantined, &value.SPF, &value.DKIM)
	return value, err
}
func mailListCursor(r *http.Request) (mailCursor, error) {
	return decodeMailCursor(r.URL.Query().Get("cursor"))
}

func (s *Server) ownerMailMessages(w http.ResponseWriter, r *http.Request, p Principal) {
	box, ok := s.ownerMailBox(w, r, p)
	if !ok {
		return
	}
	folder := r.URL.Query().Get("folder")
	if folder == "" {
		folder = "all"
	}
	if folder != "all" && folder != "unread" && folder != "quarantine" {
		writeError(w, 400, fmt.Errorf("invalid mail folder"))
		return
	}
	cursor, err := mailListCursor(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT `+mailMessageFields+` FROM mail_messages WHERE account_id=$1 AND box_id=$2 AND expires_at>now()
  AND ($3='all' OR ($3='unread' AND read_at IS NULL AND NOT quarantined) OR ($3='quarantine' AND quarantined))
  AND ($4::timestamptz IS NULL OR (received_at,id::text)<($4::timestamptz,$5)) ORDER BY received_at DESC,id DESC LIMIT 51`, p.AccountID, box.ID, folder, nullableMailCursorTime(cursor.At), cursor.ID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail list unavailable"))
		return
	}
	defer rows.Close()
	messages := []mailMessageRow{}
	for rows.Next() {
		value, err := scanMailMessage(rows)
		if err != nil {
			writeError(w, 500, fmt.Errorf("mail list unavailable"))
			return
		}
		messages = append(messages, value)
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, fmt.Errorf("mail list unavailable"))
		return
	}
	next := ""
	if len(messages) > 50 {
		messages = messages[:50]
		last := messages[len(messages)-1]
		next = encodeMailCursor(last.ReceivedAt, last.ID)
	}
	writeJSON(w, 200, map[string]any{"messages": messages, "nextCursor": next})
}
func nullableMailCursorTime(at time.Time) any {
	if at.IsZero() {
		return nil
	}
	return at
}

func (s *Server) ownerMailMessage(w http.ResponseWriter, r *http.Request, p Principal) {
	box, ok := s.ownerMailBox(w, r, p)
	if !ok {
		return
	}
	if !mailUUIDPattern.MatchString(r.PathValue("mid")) {
		writeError(w, 404, fmt.Errorf("mail message unavailable"))
		return
	}
	row := s.Store.DB.QueryRowContext(r.Context(), `SELECT `+mailMessageFields+`,text_body FROM mail_messages WHERE account_id=$1 AND box_id=$2 AND id=$3 AND expires_at>now()`, p.AccountID, box.ID, r.PathValue("mid"))
	var value mailMessageDetail
	err := row.Scan(&value.ID, &value.From, &value.FromName, &value.Subject, &value.Preview, &value.ReceivedAt, &value.Unread, &value.HasAttachments, &value.Quarantined, &value.SPF, &value.DKIM, &value.Text)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, fmt.Errorf("mail message unavailable"))
		return
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail message unavailable"))
		return
	}
	value.Attachments = []mailAttachmentRow{}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT id::text,name,content_type,size_bytes FROM mail_attachments WHERE account_id=$1 AND message_id=$2 ORDER BY id`, p.AccountID, value.ID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail attachments unavailable"))
		return
	}
	defer rows.Close()
	for rows.Next() {
		var attachment mailAttachmentRow
		if err := rows.Scan(&attachment.ID, &attachment.Name, &attachment.ContentType, &attachment.Size); err != nil {
			writeError(w, 500, fmt.Errorf("mail attachments unavailable"))
			return
		}
		value.Attachments = append(value.Attachments, attachment)
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, fmt.Errorf("mail attachments unavailable"))
		return
	}
	writeJSON(w, 200, value)
}

func (s *Server) ownerMailRead(w http.ResponseWriter, r *http.Request, p Principal) {
	box, ok := s.ownerMailBox(w, r, p)
	if !ok {
		return
	}
	if !mailUUIDPattern.MatchString(r.PathValue("mid")) {
		writeError(w, 404, fmt.Errorf("mail message unavailable"))
		return
	}
	var request struct {
		Read *bool `json:"read"`
	}
	if err := decodeJSON(r, &request); err != nil || request.Read == nil {
		writeError(w, 400, fmt.Errorf("read must be true or false"))
		return
	}
	var unread bool
	err := s.Store.DB.QueryRowContext(r.Context(), `UPDATE mail_messages SET read_at=CASE WHEN $4::bool THEN now() ELSE NULL END WHERE account_id=$1 AND box_id=$2 AND id=$3 AND expires_at>now() RETURNING read_at IS NULL`, p.AccountID, box.ID, r.PathValue("mid"), *request.Read).Scan(&unread)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, fmt.Errorf("mail message unavailable"))
		return
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("read state unavailable"))
		return
	}
	writeJSON(w, 200, map[string]any{"id": r.PathValue("mid"), "unread": unread})
}

func (s *Server) ownerMailAttachment(w http.ResponseWriter, r *http.Request, p Principal) {
	box, ok := s.ownerMailBox(w, r, p)
	if !ok {
		return
	}
	if !mailUUIDPattern.MatchString(r.PathValue("mid")) || !mailUUIDPattern.MatchString(r.PathValue("aid")) {
		writeError(w, 404, fmt.Errorf("mail attachment unavailable"))
		return
	}
	var name, contentType, scanState string
	var data []byte
	var quarantined bool
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT a.name,a.content_type,a.data,a.scan_state,m.quarantined FROM mail_attachments a JOIN mail_messages m ON m.id=a.message_id AND m.account_id=a.account_id WHERE a.account_id=$1 AND m.box_id=$2 AND m.id=$3 AND a.id=$4 AND m.expires_at>now()`, p.AccountID, box.ID, r.PathValue("mid"), r.PathValue("aid")).Scan(&name, &contentType, &data, &scanState, &quarantined)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, fmt.Errorf("mail attachment unavailable"))
		return
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail attachment unavailable"))
		return
	}
	if quarantined || scanState != "type_checked" {
		writeError(w, 423, fmt.Errorf("mail attachment quarantined"))
		return
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) ownerMailOutbox(w http.ResponseWriter, r *http.Request, p Principal) {
	box, ok := s.ownerMailBox(w, r, p)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "pending_approval" && status != "sent" && status != "rejected" && status != "failed" {
		writeError(w, 400, fmt.Errorf("invalid outbox status"))
		return
	}
	cursor, err := mailListCursor(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT id::text,recipient_list,subject,reviewed_text,status,reason,version,created_at,decided_at,sent_at FROM mail_outbox WHERE account_id=$1 AND box_id=$2 AND ($3='' OR status=$3) AND ($4::timestamptz IS NULL OR (created_at,id::text)<($4::timestamptz,$5)) ORDER BY created_at DESC,id DESC LIMIT 51`, p.AccountID, box.ID, status, nullableMailCursorTime(cursor.At), cursor.ID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("outbox unavailable"))
		return
	}
	defer rows.Close()
	items := []mailOutboxRow{}
	for rows.Next() {
		item, err := scanMailOutbox(rows)
		if err != nil {
			writeError(w, 500, fmt.Errorf("outbox unavailable"))
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, fmt.Errorf("outbox unavailable"))
		return
	}
	next := ""
	if len(items) > 50 {
		items = items[:50]
		last := items[49]
		next = encodeMailCursor(last.SubmittedAt, last.OutboxID)
	}
	writeJSON(w, 200, map[string]any{"items": items, "nextCursor": next})
}
func scanMailOutbox(scanner interface{ Scan(...any) error }) (mailOutboxRow, error) {
	var item mailOutboxRow
	var recipients []byte
	err := scanner.Scan(&item.OutboxID, &recipients, &item.Subject, &item.Text, &item.Status, &item.Reason, &item.Version, &item.SubmittedAt, &item.DecidedAt, &item.SentAt)
	if err == nil {
		err = json.Unmarshal(recipients, &item.To)
	}
	return item, err
}

func (s *Server) ownerMailApprovals(w http.ResponseWriter, r *http.Request, p Principal) {
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT o.box_id::text,b.name,o.id::text,o.recipient_list,o.subject,o.created_at FROM mail_outbox o JOIN logical_boxes b ON b.id=o.box_id AND b.account_id=o.account_id WHERE o.account_id=$1 AND o.status='pending_approval' ORDER BY o.created_at DESC LIMIT 101`, p.AccountID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail approvals unavailable"))
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var boxID, boxName, outboxID, subject string
		var toRaw []byte
		var createdAt time.Time
		if err := rows.Scan(&boxID, &boxName, &outboxID, &toRaw, &subject, &createdAt); err != nil {
			writeError(w, 500, fmt.Errorf("mail approvals unavailable"))
			return
		}
		var to []string
		if json.Unmarshal(toRaw, &to) != nil {
			writeError(w, 500, fmt.Errorf("mail approvals unavailable"))
			return
		}
		items = append(items, map[string]any{"boxId": boxID, "boxName": boxName, "outboxId": outboxID, "to": to, "subject": subject, "createdAt": createdAt})
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, fmt.Errorf("mail approvals unavailable"))
		return
	}
	var pending int
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM mail_outbox WHERE account_id=$1 AND status='pending_approval'`, p.AccountID).Scan(&pending); err != nil {
		writeError(w, 500, fmt.Errorf("mail approvals unavailable"))
		return
	}
	writeJSON(w, 200, map[string]any{"pending": pending, "items": items})
}
