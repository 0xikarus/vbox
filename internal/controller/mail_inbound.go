package controller

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const maxInboundMailBytes = 25 << 20

func verifyInboundMailHMAC(now time.Time, secret, timestamp, signature string, body []byte) error {
	if secret == "" {
		return fmt.Errorf("inbound mail is disabled")
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || seconds < 1 {
		return fmt.Errorf("invalid mail timestamp")
	}
	delta := now.Unix() - seconds
	if delta < -300 || delta > 300 {
		return fmt.Errorf("expired mail timestamp")
	}
	supplied, err := hex.DecodeString(signature)
	if err != nil || len(supplied) != sha256.Size || signature != strings.ToLower(signature) {
		return fmt.Errorf("invalid mail signature")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = io.WriteString(mac, timestamp+".")
	_, _ = mac.Write(body)
	if !hmac.Equal(supplied, mac.Sum(nil)) {
		return fmt.Errorf("invalid mail signature")
	}
	return nil
}

func readInboundMailRequest(w http.ResponseWriter, r *http.Request, limit int, requireIdempotency bool) ([]byte, bool) {
	if mailDomain() == "" || os.Getenv("VMBOX_INBOUND_MAIL_SECRET") == "" {
		writeError(w, 503, fmt.Errorf("inbound mail is disabled"))
		return nil, false
	}
	key := r.Header.Get("X-Vbox-Idempotency")
	if requireIdempotency && (len(key) != 64 || !lowerHexMailKey(key)) {
		writeError(w, 400, fmt.Errorf("invalid mail idempotency key"))
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, int64(limit)+1))
	if err != nil {
		writeError(w, 400, fmt.Errorf("invalid mail request"))
		return nil, false
	}
	if len(raw) > limit {
		writeError(w, 413, fmt.Errorf("mail request too large"))
		return nil, false
	}
	if err := verifyInboundMailHMAC(time.Now().UTC(), os.Getenv("VMBOX_INBOUND_MAIL_SECRET"), r.Header.Get("X-Vbox-Timestamp"), r.Header.Get("X-Vbox-Signature"), raw); err != nil {
		writeError(w, 401, err)
		return nil, false
	}
	return raw, true
}

func (s *Server) inboundMailRecipient(w http.ResponseWriter, r *http.Request) {
	raw, ok := readInboundMailRequest(w, r, 2048, false)
	if !ok {
		return
	}
	var request struct {
		To string `json:"to"`
	}
	if jsonUnmarshalStrict(raw, &request) != nil {
		writeError(w, 400, fmt.Errorf("invalid recipient request"))
		return
	}
	recipient := strings.ToLower(strings.TrimSpace(request.To))
	if !validMailAddress(recipient) || !strings.HasSuffix(recipient, "@"+mailDomain()) {
		writeError(w, 404, fmt.Errorf("unknown recipient"))
		return
	}
	_, exists, err := s.resolveInboundMailRecipient(r.Context(), recipient)
	if err != nil {
		writeError(w, 503, fmt.Errorf("recipient lookup unavailable"))
		return
	}
	if !exists {
		writeError(w, 404, fmt.Errorf("unknown recipient"))
		return
	}
	writeJSON(w, 200, map[string]bool{"accept": true})
}

type inboundMailDestination struct{ accountID, boxID, addressID, boxName string }

func (s *Server) resolveInboundMailRecipient(ctx context.Context, recipient string) (inboundMailDestination, bool, error) {
	var destination inboundMailDestination
	var boxID sql.NullString
	var enabled bool
	var boxState sql.NullString
	err := s.Store.DB.QueryRowContext(ctx, `SELECT a.account_id::text,a.id::text,a.owning_box_id::text,COALESCE(b.name,'Unassigned'),a.enabled,b.state FROM mail_addresses a LEFT JOIN logical_boxes b ON b.id=a.owning_box_id AND b.account_id=a.account_id WHERE lower(a.address)=$1`, recipient).Scan(&destination.accountID, &destination.addressID, &boxID, &destination.boxName, &enabled, &boxState)
	if err == nil {
		if !enabled || (boxID.Valid && (!boxState.Valid || boxState.String == "deleting" || boxState.String == "deleted")) {
			return inboundMailDestination{}, false, nil
		}
		destination.boxID = boxID.String
		return destination, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return destination, false, err
	}
	err = s.Store.DB.QueryRowContext(ctx, `SELECT account_id::text FROM mail_account_settings WHERE keep_unknown=true`).Scan(&destination.accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return destination, false, nil
	}
	if err != nil {
		return destination, false, err
	}
	destination.boxName = "Unassigned"
	return destination, true, nil
}

func (s *Server) inboundMailMessage(w http.ResponseWriter, r *http.Request) {
	raw, ok := readInboundMailRequest(w, r, maxInboundMailBytes, true)
	if !ok {
		return
	}
	recipient := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Vbox-Rcpt")))
	envelopeFrom := strings.TrimSpace(r.Header.Get("X-Vbox-From"))
	if !validMailAddress(recipient) || !strings.HasSuffix(recipient, "@"+mailDomain()) || len(envelopeFrom) > 254 || strings.ContainsAny(envelopeFrom, "\r\n") {
		writeError(w, 400, fmt.Errorf("invalid mail envelope"))
		return
	}
	key := r.Header.Get("X-Vbox-Idempotency")
	ingestKey := recipient + ":" + key
	digest := sha256.Sum256(raw)
	rawHash := hex.EncodeToString(digest[:])
	var priorHash, priorRecipient, priorID string
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT raw_sha256,envelope_to,id::text FROM mail_messages WHERE ingest_key=$1`, ingestKey).Scan(&priorHash, &priorRecipient, &priorID)
	if err == nil {
		writeInboundDuplicate(w, priorHash, priorRecipient, rawHash, recipient, priorID)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 503, fmt.Errorf("mail store unavailable"))
		return
	}
	destination, found, err := s.resolveInboundMailRecipient(r.Context(), recipient)
	if !found && err == nil {
		writeError(w, 404, fmt.Errorf("unknown recipient"))
		return
	}
	if err != nil {
		writeError(w, 503, fmt.Errorf("recipient lookup unavailable"))
		return
	}
	parsed, err := parseInboundMIME(raw)
	if err != nil {
		parsed = parsedInboundMail{Text: "[Unreadable MIME message]", Preview: "Unreadable MIME message", SPF: "unknown", DKIM: "unknown", Quarantined: true}
	}
	quarantined := parsed.Quarantined
	id := uuid()
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 503, fmt.Errorf("mail store unavailable"))
		return
	}
	defer tx.Rollback()
	var inserted string
	err = tx.QueryRowContext(r.Context(), `INSERT INTO mail_messages(id,account_id,box_id,address_id,ingest_key,raw_sha256,rfc_message_id,envelope_from,envelope_to,header_from,from_name,subject,text_body,preview,quarantined,spf,dkim,has_attachments) VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18) ON CONFLICT(ingest_key) DO NOTHING RETURNING id::text`, id, destination.accountID, destination.boxID, destination.addressID, ingestKey, rawHash, parsed.MessageID, envelopeFrom, recipient, parsed.From, parsed.FromName, parsed.Subject, parsed.Text, parsed.Preview, quarantined, parsed.SPF, parsed.DKIM, len(parsed.Attachments) > 0).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.QueryRowContext(r.Context(), `SELECT raw_sha256,envelope_to,id::text FROM mail_messages WHERE ingest_key=$1`, ingestKey).Scan(&priorHash, &priorRecipient, &priorID); err != nil {
			writeError(w, 503, fmt.Errorf("mail store unavailable"))
			return
		}
		writeInboundDuplicate(w, priorHash, priorRecipient, rawHash, recipient, priorID)
		return
	}
	if err != nil {
		writeError(w, 503, fmt.Errorf("mail store unavailable"))
		return
	}
	for _, attachment := range parsed.Attachments {
		scanState := "type_checked"
		if attachment.Blocked {
			scanState = "blocked"
		}
		_, err := tx.ExecContext(r.Context(), `INSERT INTO mail_attachments(id,account_id,message_id,name,content_type,size_bytes,sha256,data,scan_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, uuid(), destination.accountID, id, attachment.Name, attachment.ContentType, len(attachment.Data), attachment.SHA256, attachment.Data, scanState)
		if err != nil {
			writeError(w, 503, fmt.Errorf("mail attachment store unavailable"))
			return
		}
	}
	if err := appendMailEvent(r.Context(), tx, destination.accountID, destination.boxID, id, "", "ingest", "worker", nil); err != nil {
		writeError(w, 503, fmt.Errorf("mail audit unavailable"))
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, 503, fmt.Errorf("mail store unavailable"))
		return
	}
	s.pushAccountNotification(destination.accountID, map[string]string{"title": "New mail · " + destination.boxName, "body": "A message arrived for " + recipient + ".", "box": destination.boxID, "url": "/mail"})
	writeJSON(w, 202, map[string]string{"id": id})
}

func lowerHexMailKey(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func writeInboundDuplicate(w http.ResponseWriter, oldHash, oldRecipient, newHash, newRecipient, id string) {
	if oldHash != newHash || !strings.EqualFold(oldRecipient, newRecipient) {
		writeError(w, 409, fmt.Errorf("mail idempotency key already used"))
		return
	}
	writeJSON(w, 202, map[string]string{"id": id})
}

func jsonUnmarshalStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return fmt.Errorf("extra JSON data")
	}
	return nil
}
