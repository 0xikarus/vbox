package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/notifications"
)

func (s *Server) replyTelegramOwner(ctx context.Context, id CoworkerIdentity, sequence int64, text string) (any, error) {
	if sequence <= 0 || strings.TrimSpace(text) == "" || len(text) > 3500 {
		return nil, fmt.Errorf("positive event sequence and reply of 1–3500 bytes required")
	}
	var raw []byte
	err := s.Store.DB.QueryRowContext(ctx, `SELECT e.data FROM coworker_events e JOIN coworkers c ON c.account_id=e.account_id AND c.box_id=e.recipient_box_id JOIN coworker_settings g ON g.account_id=e.account_id WHERE e.sequence=$1 AND e.account_id=$2 AND e.recipient_box_id=$3 AND e.kind='owner_message' AND c.enabled AND g.enabled`, sequence, id.AccountID, id.BoxID).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("owner message unavailable to this coworker")
	}
	var event struct {
		UserID   string              `json:"userId"`
		Telegram *telegramReplyRoute `json:"telegram"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Telegram == nil {
		return nil, fmt.Errorf("message has no Telegram reply route")
	}
	destination, err := s.Store.Notification(ctx, id.AccountID, "telegram", event.Telegram.Destination)
	if err != nil || !destination.Enabled {
		return nil, fmt.Errorf("Telegram destination unavailable")
	}
	var secret, config map[string]any
	if json.Unmarshal(destination.Secret, &secret) != nil || json.Unmarshal(destination.Config, &config) != nil {
		return nil, fmt.Errorf("invalid Telegram destination")
	}
	chatAllowed, ownerAllowed := false, false
	for _, chat := range destination.AllowedChats {
		if chat == strconv.FormatInt(event.Telegram.ChatID, 10) {
			chatAllowed = true
		}
	}
	mapping := stringMap(config["userMap"])
	for _, user := range destination.AllowedUsers {
		if mapping[user] == event.UserID {
			ownerAllowed = true
		}
	}
	principal, err := s.Store.IntegrationPrincipal(ctx, id.AccountID, event.UserID)
	if err != nil || principal.Role != "owner" || !chatAllowed || !ownerAllowed {
		return nil, fmt.Errorf("Telegram reply authorization revoked")
	}
	// Claim before the external send. A crash or timeout after this point is
	// ambiguous: never replay automatically and risk a duplicate message.
	var claimed int64
	err = s.Store.DB.QueryRowContext(ctx, `INSERT INTO coworker_telegram_replies(event_sequence,body,state) VALUES($1,$2,'sending') ON CONFLICT DO NOTHING RETURNING event_sequence`, sequence, text).Scan(&claimed)
	if errors.Is(err, sql.ErrNoRows) {
		var body, state string
		if err = s.Store.DB.QueryRowContext(ctx, `SELECT body,state FROM coworker_telegram_replies WHERE event_sequence=$1`, sequence).Scan(&body, &state); err != nil {
			return nil, fmt.Errorf("reply status unavailable")
		}
		if body != text {
			return nil, fmt.Errorf("this event already has a different reply")
		}
		if state != "sent" {
			return nil, fmt.Errorf("reply delivery unconfirmed; inspect Telegram before retrying")
		}
		return map[string]any{"sequence": sequence, "state": "sent"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not record reply intent")
	}
	adapter := notifications.Telegram{Token: stringField(secret, "token"), ChatID: event.Telegram.ChatID, Client: s.HTTP}
	sendErr := adapter.Send(ctx, notifications.Delivery{Box: id.BoxID, State: "reply", Message: text})
	state := "sent"
	if sendErr != nil {
		state = "uncertain"
	}
	settle, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, recordErr := s.Store.DB.ExecContext(settle, `UPDATE coworker_telegram_replies SET state=$2,updated_at=now() WHERE event_sequence=$1`, sequence, state)
	if sendErr != nil || recordErr != nil {
		return nil, fmt.Errorf("reply delivery unconfirmed; inspect Telegram before retrying")
	}
	return map[string]any{"sequence": sequence, "state": "sent"}, nil
}
