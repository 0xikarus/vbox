package controller

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/secrets"
)

type CoworkerIdentity struct{ AccountID, BoxID string }

// Allocate event sequences only while holding the account gate lock through
// commit. Otherwise sequence N+1 can become visible before N, and cursor-based
// consumers would permanently skip N after advancing past it.
func (s *Store) beginCoworkerWrite(ctx context.Context, account string) (*sql.Tx, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT enabled FROM coworker_settings WHERE account_id=$1 FOR UPDATE`, account).Scan(&enabled); err != nil || !enabled {
		tx.Rollback()
		return nil, fmt.Errorf("coworker gate disabled")
	}
	return tx, nil
}

type CoworkerEvent struct {
	Sequence  int64           `json:"sequence"`
	Sender    string          `json:"sender"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Both an account gate and explicit per-box enrollment are required.
func (s *Store) EnableCoworkers(ctx context.Context, p Principal, enabled bool) error {
	if p.Role != "owner" {
		return fmt.Errorf("account owner required")
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO coworker_settings(account_id,enabled) VALUES($1,$2) ON CONFLICT(account_id) DO UPDATE SET enabled=excluded.enabled`, p.AccountID, enabled)
	return err
}

func (s *Store) EnrollCoworker(ctx context.Context, p Principal, boxID string) error {
	if p.Role != "owner" || s.Envelope == nil {
		return fmt.Errorf("account owner and credential encryption required")
	}
	box, err := s.LogicalBox(ctx, p, boxID)
	if err != nil {
		return fmt.Errorf("logical box not found")
	}
	if !strings.HasPrefix(box.Name, "coworker-") {
		return fmt.Errorf("explicit coworker- box name required")
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return err
	}
	defer clear(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	sealed, err := s.Envelope.Seal(p.AccountID+":coworker:"+box.ID, []byte(token))
	if err != nil {
		return fmt.Errorf("coworker token encryption failed")
	}
	result, err := s.DB.ExecContext(ctx, `INSERT INTO coworkers(account_id,box_id,token_hash,encrypted_token) SELECT $1,$2,$3,$4 FROM coworker_settings WHERE account_id=$1 AND enabled=true ON CONFLICT(account_id,box_id) DO NOTHING`, p.AccountID, box.ID, secrets.TokenHash(token), sealed)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fmt.Errorf("enable account coworkers first; already enrolled boxes are not silently rekeyed")
	}
	return nil
}

func (s *Store) AuthenticateCoworker(ctx context.Context, token string) (CoworkerIdentity, error) {
	var id CoworkerIdentity
	if token == "" {
		return id, fmt.Errorf("coworker token required")
	}
	err := s.DB.QueryRowContext(ctx, `SELECT c.account_id::text,c.box_id::text FROM coworkers c JOIN coworker_settings g ON g.account_id=c.account_id JOIN logical_boxes b ON b.id=c.box_id AND b.account_id=c.account_id WHERE c.token_hash=$1 AND c.enabled AND g.enabled AND b.state='running'`, secrets.TokenHash(token)).Scan(&id.AccountID, &id.BoxID)
	if err != nil {
		return id, fmt.Errorf("invalid or inactive coworker token")
	}
	return id, nil
}

func (s *Store) SendCoworkerMessage(ctx context.Context, sender CoworkerIdentity, recipient, key, body string) (int64, error) {
	if strings.HasPrefix(key, "board:") {
		return 0, fmt.Errorf("message key prefix board: is reserved for board events")
	}
	if key == "" || len(key) > 128 || strings.TrimSpace(body) == "" || len(body) > 16384 {
		return 0, fmt.Errorf("message requires a key (up to 128 bytes) and text (up to 16 KiB)")
	}
	data, _ := json.Marshal(map[string]string{"text": body})
	tx, err := s.beginCoworkerWrite(ctx, sender.AccountID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var sequence int64
	err = tx.QueryRowContext(ctx, `INSERT INTO coworker_events(account_id,recipient_box_id,sender_box_id,message_key,kind,data) SELECT $1,target.box_id,source.box_id,$4,'message',$5 FROM coworkers source JOIN coworkers target ON target.account_id=source.account_id JOIN coworker_settings g ON g.account_id=source.account_id WHERE source.account_id=$1 AND source.box_id=$2 AND target.box_id=$3 AND source.enabled AND target.enabled AND g.enabled ON CONFLICT(account_id,sender_box_id,message_key) DO UPDATE SET message_key=excluded.message_key WHERE coworker_events.recipient_box_id=excluded.recipient_box_id AND coworker_events.data=excluded.data RETURNING sequence`, sender.AccountID, sender.BoxID, recipient, key, data).Scan(&sequence)
	if err != nil {
		return 0, fmt.Errorf("message rejected: inactive coworker, wrong account, or reused key with different content")
	}
	return sequence, tx.Commit()
}

func (s *Store) CoworkerInbox(ctx context.Context, id CoworkerIdentity, after int64) ([]CoworkerEvent, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT sequence,COALESCE(sender_box_id::text,'owner'),kind,data,created_at FROM coworker_events WHERE account_id=$1 AND recipient_box_id=$2 AND sequence>$3 ORDER BY sequence LIMIT 100`, id.AccountID, id.BoxID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CoworkerEvent{}
	for rows.Next() {
		var e CoworkerEvent
		if err := rows.Scan(&e.Sequence, &e.Sender, &e.Kind, &e.Data, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
