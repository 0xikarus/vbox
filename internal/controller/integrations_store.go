package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

func (s *Store) Notification(ctx context.Context, accountID, kind, name string) (DecryptedNotification, error) {
	var value DecryptedNotification
	var encrypted string
	var users, chats []byte
	err := s.DB.QueryRowContext(ctx, `SELECT id::text,account_id::text,kind,name,encrypted_secret,config,allowed_users,allowed_chats,enabled,created_at,updated_at FROM notification_destinations WHERE account_id=$1 AND kind=$2 AND name=$3 AND enabled=true`, accountID, kind, name).Scan(&value.ID, &value.AccountID, &value.Kind, &value.Name, &encrypted, &value.Config, &users, &chats, &value.Enabled, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return value, fmt.Errorf("notification destination not found")
	}
	if err != nil {
		return value, err
	}
	if s.Envelope == nil {
		return value, fmt.Errorf("controller encryption key is not configured")
	}
	if err := json.Unmarshal(users, &value.AllowedUsers); err != nil {
		return value, err
	}
	if err := json.Unmarshal(chats, &value.AllowedChats); err != nil {
		return value, err
	}
	plain, err := s.Envelope.Open(accountID, encrypted)
	if err != nil {
		return value, fmt.Errorf("decrypt notification destination: %w", err)
	}
	value.Secret = append(json.RawMessage(nil), plain...)
	return value, nil
}

func (s *Store) IntegrationPrincipal(ctx context.Context, accountID, userID string) (Principal, error) {
	p := Principal{AccountID: accountID, UserID: userID}
	err := s.DB.QueryRowContext(ctx, `SELECT role,subject FROM users WHERE account_id=$1 AND id=$2 AND disabled_at IS NULL`, accountID, userID).Scan(&p.Role, &p.Subject)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, fmt.Errorf("mapped controller user is unavailable")
	}
	return p, err
}
