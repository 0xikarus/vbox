package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/notifications"
)

func (s *Store) SendOwnerCoworkerMessage(ctx context.Context, p Principal, box, key, text string) (int64, error) {
	if p.Role != "owner" || key == "" || len(key) > 128 || strings.TrimSpace(text) == "" || len(text) > 16384 {
		return 0, fmt.Errorf("owner, retry key, and message up to 16 KiB required")
	}
	data, _ := json.Marshal(map[string]string{"text": text, "userId": p.UserID})
	tx, err := s.beginCoworkerWrite(ctx, p.AccountID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var seq int64
	err = tx.QueryRowContext(ctx, `INSERT INTO coworker_events(account_id,recipient_box_id,message_key,kind,data) SELECT c.account_id,c.box_id,$3,'owner_message',$4 FROM coworkers c JOIN logical_boxes b ON b.id=c.box_id AND b.account_id=c.account_id JOIN coworker_settings g ON g.account_id=c.account_id WHERE c.account_id=$1 AND (b.id::text=$2 OR b.name=$2) AND c.enabled AND g.enabled ON CONFLICT(account_id,message_key) WHERE sender_box_id IS NULL DO UPDATE SET message_key=excluded.message_key WHERE coworker_events.recipient_box_id=excluded.recipient_box_id AND coworker_events.data=excluded.data RETURNING sequence`, p.AccountID, box, key, data).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("message not accepted; check coworker gate, box, and retry key")
	}
	return seq, tx.Commit()
}

func (s *Server) telegramCoworkerCommand(value DecryptedNotification, secret map[string]any) func(context.Context, string, int64, int64, string) error {
	return func(ctx context.Context, userID string, updateID, chatID int64, text string) error {
		p, err := s.Store.IntegrationPrincipal(ctx, value.AccountID, userID)
		if err != nil || p.Role != "owner" {
			return fmt.Errorf("coworker Telegram commands require a mapped controller owner")
		}
		fields := strings.SplitN(strings.TrimSpace(text), " ", 3)
		message := ""
		switch fields[0] {
		case "/coworker":
			if len(fields) != 3 {
				return fmt.Errorf("usage: /coworker BOX TEXT")
			}
			seq, err := s.Store.SendOwnerCoworkerMessage(ctx, p, fields[1], fmt.Sprintf("telegram:%s:%d", value.ID, updateID), fields[2])
			if err != nil {
				return err
			}
			message = fmt.Sprintf("Message %d queued durably.", seq)
		case "/coworker-messages":
			if len(fields) != 2 {
				return fmt.Errorf("usage: /coworker-messages BOX")
			}
			box, err := s.Store.LogicalBox(ctx, p, fields[1])
			if err != nil {
				return err
			}
			rows, err := s.Store.DB.QueryContext(ctx, `SELECT sequence,data->>'text' FROM coworker_events WHERE account_id=$1 AND sender_box_id=$2 AND kind='message' ORDER BY sequence DESC LIMIT 5`, p.AccountID, box.ID)
			if err != nil {
				return fmt.Errorf("coworker messages unavailable")
			}
			defer rows.Close()
			message = "Latest outgoing messages (newest first):\n"
			for rows.Next() {
				var seq int64
				var body string
				if err = rows.Scan(&seq, &body); err != nil {
					return err
				}
				if len(body) > 400 {
					body = string([]rune(body)[:min(100, len([]rune(body)))]) + "…"
				}
				message += fmt.Sprintf("%d: %s\n", seq, body)
			}
			if rows.Err() != nil {
				return fmt.Errorf("coworker message read interrupted")
			}
		default:
			return fmt.Errorf("unsupported coworker command")
		}
		n := notifications.Telegram{Token: stringField(secret, "token"), ChatID: chatID, Client: s.HTTP}
		if err := n.Send(ctx, notifications.Delivery{Box: "coworkers", Message: message}); err != nil {
			return fmt.Errorf("command processed; Telegram response failed")
		}
		return nil
	}
}
