package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func (s *Store) SendOwnerCoworkerMessage(ctx context.Context, p Principal, box, key, text string) (int64, error) {
	if p.Role != "owner" || key == "" || len(key) > 128 || strings.TrimSpace(text) == "" || len(text) > 16384 {
		return 0, fmt.Errorf("owner, retry key, and message up to 16 KiB required")
	}
	fields := map[string]any{"text": text, "userId": p.UserID}
	data, _ := json.Marshal(fields)
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
