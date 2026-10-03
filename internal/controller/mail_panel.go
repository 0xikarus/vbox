package controller

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
)

type mailPanelMessageRow struct {
	mailMessageRow
	BoxID      string `json:"boxId"`
	BoxName    string `json:"boxName"`
	BoxAddress string `json:"boxAddress"`
	Address    string `json:"address"`
	AddressID  string `json:"addressId"`
}

type mailPanelOutboxRow struct {
	mailOutboxRow
	BoxID      string `json:"boxId"`
	BoxName    string `json:"boxName"`
	BoxAddress string `json:"boxAddress"`
}

func panelMailBoxFilter(r *http.Request) (string, error) {
	box := strings.TrimSpace(r.URL.Query().Get("box"))
	if box != "" && !mailUUIDPattern.MatchString(box) {
		return "", fmt.Errorf("invalid box filter")
	}
	return box, nil
}

func (s *Server) ownerMailPanelMessages(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := panelMailBoxFilter(r)
	if err != nil {
		writeError(w, 400, err)
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
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 200 {
		writeError(w, 400, fmt.Errorf("mail search is too long"))
		return
	}
	address := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("address")))
	if address != "" && address != "unassigned" && !validMailAddress(address) && !mailUUIDPattern.MatchString(address) {
		writeError(w, 400, fmt.Errorf("invalid address filter"))
		return
	}
	cursor, err := mailListCursor(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT m.id::text,m.header_from,m.from_name,m.subject,m.preview,m.received_at,m.read_at IS NULL,m.has_attachments,m.quarantined,m.spf,m.dkim,COALESCE(m.box_id::text,''),COALESCE(b.name,'Unassigned'),COALESCE(ms.address,''),m.envelope_to,COALESCE(m.address_id::text,'')
 FROM mail_messages m LEFT JOIN logical_boxes b ON b.id=m.box_id AND b.account_id=m.account_id
 LEFT JOIN box_mail_settings ms ON ms.box_id=m.box_id AND ms.account_id=m.account_id
 WHERE m.account_id=$1 AND m.expires_at>now() AND ($2='' OR m.box_id::text=$2)
 AND (($3='all' AND NOT m.quarantined) OR ($3='unread' AND m.read_at IS NULL AND NOT m.quarantined) OR ($3='quarantine' AND m.quarantined))
 AND ($4='' OR position(lower($4) in lower(m.header_from||' '||m.from_name||' '||m.subject||' '||m.text_body))>0)
 AND ($5::timestamptz IS NULL OR (m.received_at,m.id::text)<($5::timestamptz,$6))
 AND ($7='' OR ($7='unassigned' AND m.address_id IS NULL) OR lower(m.envelope_to)=$7 OR m.address_id::text=$7)
 ORDER BY m.received_at DESC,m.id DESC LIMIT 51`, p.AccountID, box, folder, q, nullableMailCursorTime(cursor.At), cursor.ID, address)
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail list unavailable"))
		return
	}
	defer rows.Close()
	messages := []mailPanelMessageRow{}
	for rows.Next() {
		var value mailPanelMessageRow
		if err := rows.Scan(&value.ID, &value.From, &value.FromName, &value.Subject, &value.Preview, &value.ReceivedAt, &value.Unread, &value.HasAttachments, &value.Quarantined, &value.SPF, &value.DKIM, &value.BoxID, &value.BoxName, &value.BoxAddress, &value.Address, &value.AddressID); err != nil {
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
		last := messages[49]
		next = encodeMailCursor(last.ReceivedAt, last.ID)
	}
	writeJSON(w, 200, map[string]any{"messages": messages, "nextCursor": next})
}

func (s *Server) panelMailBoxForMessage(w http.ResponseWriter, r *http.Request, p Principal) (mailPanelMessageRow, bool) {
	var box mailPanelMessageRow
	id := r.PathValue("mid")
	if !mailUUIDPattern.MatchString(id) {
		writeError(w, 404, fmt.Errorf("mail message unavailable"))
		return box, false
	}
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT COALESCE(m.box_id::text,''),COALESCE(b.name,'Unassigned'),COALESCE(ms.address,''),m.envelope_to,COALESCE(m.address_id::text,'') FROM mail_messages m LEFT JOIN logical_boxes b ON b.id=m.box_id AND b.account_id=m.account_id LEFT JOIN box_mail_settings ms ON ms.box_id=m.box_id AND ms.account_id=m.account_id WHERE m.account_id=$1 AND m.id=$2 AND m.expires_at>now()`, p.AccountID, id).Scan(&box.BoxID, &box.BoxName, &box.BoxAddress, &box.Address, &box.AddressID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, fmt.Errorf("mail message unavailable"))
		return box, false
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail message unavailable"))
		return box, false
	}
	return box, true
}

func (s *Server) ownerMailPanelMessage(w http.ResponseWriter, r *http.Request, p Principal) {
	box, ok := s.panelMailBoxForMessage(w, r, p)
	if !ok {
		return
	}
	value, err := s.Store.loadMailMessageDetail(r.Context(), p.AccountID, box.BoxID, r.PathValue("mid"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, fmt.Errorf("mail message unavailable"))
		return
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail message unavailable"))
		return
	}
	writeJSON(w, 200, struct {
		mailMessageDetail
		BoxID      string `json:"boxId"`
		BoxName    string `json:"boxName"`
		BoxAddress string `json:"boxAddress"`
		Address    string `json:"address"`
		AddressID  string `json:"addressId"`
	}{value, box.BoxID, box.BoxName, box.BoxAddress, box.Address, box.AddressID})
}

func (s *Server) ownerMailPanelRead(w http.ResponseWriter, r *http.Request, p Principal) {
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
	err := s.Store.DB.QueryRowContext(r.Context(), `UPDATE mail_messages SET read_at=CASE WHEN $3::bool THEN now() ELSE NULL END WHERE account_id=$1 AND id=$2 AND expires_at>now() RETURNING read_at IS NULL`, p.AccountID, r.PathValue("mid"), *request.Read).Scan(&unread)
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

func (s *Server) ownerMailPanelAttachment(w http.ResponseWriter, r *http.Request, p Principal) {
	if !mailUUIDPattern.MatchString(r.PathValue("mid")) || !mailUUIDPattern.MatchString(r.PathValue("aid")) {
		writeError(w, 404, fmt.Errorf("mail attachment unavailable"))
		return
	}
	var name, scanState string
	var data []byte
	var quarantined bool
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT a.name,a.data,a.scan_state,m.quarantined FROM mail_attachments a JOIN mail_messages m ON m.id=a.message_id AND m.account_id=a.account_id WHERE a.account_id=$1 AND m.id=$2 AND a.id=$3 AND m.expires_at>now()`, p.AccountID, r.PathValue("mid"), r.PathValue("aid")).Scan(&name, &data, &scanState, &quarantined)
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
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) ownerMailPanelRelease(w http.ResponseWriter, r *http.Request, p Principal) {
	if !mailUUIDPattern.MatchString(r.PathValue("mid")) {
		writeError(w, 404, fmt.Errorf("mail message unavailable"))
		return
	}
	var id string
	err := s.Store.DB.QueryRowContext(r.Context(), `UPDATE mail_messages SET quarantined=false,read_at=NULL,notified_at=NULL WHERE account_id=$1 AND id=$2 AND expires_at>now() AND quarantined RETURNING id::text`, p.AccountID, r.PathValue("mid")).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, fmt.Errorf("quarantined mail message unavailable"))
		return
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail release unavailable"))
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "quarantined": false, "unread": true})
}

func (s *Server) ownerMailPanelDelete(w http.ResponseWriter, r *http.Request, p Principal) {
	if !mailUUIDPattern.MatchString(r.PathValue("mid")) {
		writeError(w, 404, fmt.Errorf("mail message unavailable"))
		return
	}
	var id string
	err := s.Store.DB.QueryRowContext(r.Context(), `DELETE FROM mail_messages WHERE account_id=$1 AND id=$2 RETURNING id::text`, p.AccountID, r.PathValue("mid")).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, fmt.Errorf("mail message unavailable"))
		return
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail deletion unavailable"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ownerMailPanelOutbox(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := panelMailBoxFilter(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "pending_approval" && status != "sending" && status != "sent" && status != "rejected" && status != "failed" {
		writeError(w, 400, fmt.Errorf("invalid outbox status"))
		return
	}
	cursor, err := mailListCursor(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT o.id::text,o.recipient_list,COALESCE(NULLIF(o.from_address,''),ms.address,''),o.subject,o.reviewed_text,o.status,o.reason,o.version,o.created_at,o.decided_at,o.sent_at,o.box_id::text,b.name,COALESCE(ms.address,'') FROM mail_outbox o JOIN logical_boxes b ON b.id=o.box_id AND b.account_id=o.account_id LEFT JOIN box_mail_settings ms ON ms.box_id=o.box_id AND ms.account_id=o.account_id WHERE o.account_id=$1 AND ($2='' OR o.box_id::text=$2) AND ($3='' OR o.status=$3) AND ($4::timestamptz IS NULL OR (o.created_at,o.id::text)<($4::timestamptz,$5)) ORDER BY o.created_at DESC,o.id DESC LIMIT 51`, p.AccountID, box, status, nullableMailCursorTime(cursor.At), cursor.ID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("outbox unavailable"))
		return
	}
	defer rows.Close()
	items := []mailPanelOutboxRow{}
	for rows.Next() {
		var item mailPanelOutboxRow
		var recipients []byte
		if err := rows.Scan(&item.OutboxID, &recipients, &item.From, &item.Subject, &item.Text, &item.Status, &item.Reason, &item.Version, &item.SubmittedAt, &item.DecidedAt, &item.SentAt, &item.BoxID, &item.BoxName, &item.BoxAddress); err != nil {
			writeError(w, 500, fmt.Errorf("outbox unavailable"))
			return
		}
		if json.Unmarshal(recipients, &item.To) != nil {
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

func (s *Server) ownerMailPanelSummary(w http.ResponseWriter, r *http.Request, p Principal) {
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT b.id::text,b.name,COALESCE(ms.address,''),COALESCE(ms.enabled,false),count(m.id) FILTER (WHERE m.read_at IS NULL AND NOT m.quarantined AND m.expires_at>now()),count(m.id) FILTER (WHERE m.quarantined AND m.expires_at>now()) FROM logical_boxes b LEFT JOIN box_mail_settings ms ON ms.box_id=b.id AND ms.account_id=b.account_id LEFT JOIN mail_messages m ON m.box_id=b.id AND m.account_id=b.account_id WHERE b.account_id=$1 AND b.state NOT IN ('deleting','deleted') GROUP BY b.id,b.name,ms.address,ms.enabled ORDER BY b.name,b.id`, p.AccountID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail summary unavailable"))
		return
	}
	defer rows.Close()
	type summaryBox struct {
		BoxID   string `json:"boxId"`
		BoxName string `json:"boxName"`
		Address string `json:"address"`
		Enabled bool   `json:"enabled"`
		Unread  int    `json:"unread"`
	}
	boxes := []summaryBox{}
	unread, quarantine := 0, 0
	for rows.Next() {
		var item summaryBox
		var quarantined int
		if err := rows.Scan(&item.BoxID, &item.BoxName, &item.Address, &item.Enabled, &item.Unread, &quarantined); err != nil {
			writeError(w, 500, fmt.Errorf("mail summary unavailable"))
			return
		}
		boxes = append(boxes, item)
		_ = quarantined
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, fmt.Errorf("mail summary unavailable"))
		return
	}
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT count(*) FILTER (WHERE read_at IS NULL AND NOT quarantined),count(*) FILTER (WHERE quarantined) FROM mail_messages WHERE account_id=$1 AND expires_at>now()`, p.AccountID).Scan(&unread, &quarantine); err != nil {
		writeError(w, 500, fmt.Errorf("mail summary unavailable"))
		return
	}
	var pending int
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM mail_outbox WHERE account_id=$1 AND status='pending_approval'`, p.AccountID).Scan(&pending); err != nil {
		writeError(w, 500, fmt.Errorf("mail summary unavailable"))
		return
	}
	writeJSON(w, 200, map[string]any{"unread": unread, "quarantine": quarantine, "pending": pending, "boxes": boxes})
}
