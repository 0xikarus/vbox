package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/0xikarus/vmbox-service/internal/browser"
	"github.com/0xikarus/vmbox-service/internal/secrets"
)

type DesktopSecret struct {
	Key       string    `json:"key"`
	Origin    string    `json:"origin"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

func secretOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("secret destination must be an HTTPS origin")
	}
	return browser.Origin(raw)
}

func desktopSecretScope(account, box, key, origin string) string {
	return account + ":desktop-secret:" + box + ":" + key + ":" + origin
}

// EnsureDesktopSecret saves before use and never changes an existing binding.
// Concurrent calls converge on the unique account/box/key row. The plaintext
// has no representation in the returned metadata.
func (s *Store) EnsureDesktopSecret(ctx context.Context, p Principal, box, key, origin string, value []byte) (DesktopSecret, bool, error) {
	var result DesktopSecret
	if !loginProfileName.MatchString(key) {
		return result, false, fmt.Errorf("invalid secret key")
	}
	origin, err := secretOrigin(origin)
	if err != nil {
		return result, false, err
	}
	resolved, err := s.LogicalBox(ctx, p, box)
	if err != nil {
		return result, false, fmt.Errorf("box unavailable")
	}
	box = resolved.ID
	return s.ensureDesktopSecret(ctx, s.DB, p, box, key, origin, value, secrets.PasswordPolicy{})
}

type desktopSecretQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// The caller has resolved box ownership and validated the key and origin.
func (s *Store) ensureDesktopSecret(ctx context.Context, db desktopSecretQuerier, p Principal, box, key, origin string, value []byte, policy secrets.PasswordPolicy) (DesktopSecret, bool, error) {
	var result DesktopSecret
	var err error
	if s.Envelope == nil {
		return result, false, fmt.Errorf("secret encryption unavailable")
	}
	if len(value) > 4096 {
		return result, false, fmt.Errorf("secret exceeds 4096 bytes")
	}
	plain := append([]byte(nil), value...)
	if len(plain) == 0 {
		plain, err = secrets.GeneratePassword(policy.Length, policy.Alphabet)
		if err != nil {
			return result, false, err
		}
	}
	defer clear(plain)
	sealed, err := s.Envelope.Seal(desktopSecretScope(p.AccountID, box, key, origin), plain)
	if err != nil {
		return result, false, fmt.Errorf("could not encrypt secret")
	}
	result.Key, result.Origin, result.Status = key, origin, "pending"
	err = db.QueryRowContext(ctx, `INSERT INTO desktop_secrets(account_id,box_id,secret_key,origin,encrypted_value,creator_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(account_id,box_id,secret_key) DO NOTHING RETURNING created_at`, p.AccountID, box, key, origin, sealed, p.UserID).Scan(&result.CreatedAt)
	if err == nil {
		return result, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DesktopSecret{}, false, fmt.Errorf("could not save secret")
	}
	err = db.QueryRowContext(ctx, `SELECT origin,status,created_at FROM desktop_secrets WHERE account_id=$1 AND box_id=$2 AND secret_key=$3`, p.AccountID, box, key).Scan(&result.Origin, &result.Status, &result.CreatedAt)
	if err != nil || result.Origin != origin {
		return DesktopSecret{}, false, fmt.Errorf("secret reference conflicts with an existing binding")
	}
	return result, false, nil
}

func (s *Store) ListDesktopSecrets(ctx context.Context, p Principal, box string) ([]DesktopSecret, error) {
	resolved, err := s.LogicalBox(ctx, p, box)
	if err != nil {
		return nil, fmt.Errorf("box unavailable")
	}
	box = resolved.ID
	rows, err := s.DB.QueryContext(ctx, `SELECT secret_key,origin,status,created_at FROM desktop_secrets WHERE account_id=$1 AND box_id=$2 ORDER BY created_at`, p.AccountID, box)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []DesktopSecret{}
	for rows.Next() {
		var value DesktopSecret
		if err = rows.Scan(&value.Key, &value.Origin, &value.Status, &value.CreatedAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
