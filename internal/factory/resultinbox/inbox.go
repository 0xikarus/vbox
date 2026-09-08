// Package resultinbox persists unverified worker reports before workers hibernate.
// Possession of a capability authorizes one attempt's callback, not completion.
package resultinbox

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"
	"unicode/utf8"
)

const MaxBodyBytes = 400 * 1024
const DefaultTTL = 30 * time.Minute
const MaxTTL = DefaultTTL

var (
	ErrInvalid       = errors.New("resultinbox: invalid input")
	ErrUnauthorized  = errors.New("resultinbox: invalid capability")
	ErrExpired       = errors.New("resultinbox: attempt expired")
	ErrConflict      = errors.New("resultinbox: conflicting result")
	ErrNotFound      = errors.New("resultinbox: result not found")
	ErrTooLarge      = errors.New("resultinbox: body too large")
	ErrStorage       = errors.New("resultinbox: storage failure")
	ErrSecretChanged = errors.New("resultinbox: capability secret changed")
)

// Inbox is concurrency safe. The caller owns DB and must keep the same secret
// across restarts. Secret rotation requires a new attempt for outstanding work.
type Inbox struct {
	db     *sql.DB
	secret []byte
	ttl    time.Duration
}

// New copies secret, which must contain at least 32 cryptographically random
// bytes. Zero ttl selects DefaultTTL; other values must be in (0, MaxTTL].
func New(db *sql.DB, secret []byte, ttl time.Duration) (*Inbox, error) {
	if db == nil || len(secret) < 32 || ttl < 0 || ttl > MaxTTL {
		return nil, ErrInvalid
	}
	if ttl == 0 {
		ttl = DefaultTTL
	}
	return &Inbox{db: db, secret: bytes.Clone(secret), ttl: ttl}, nil
}

// Migrate creates this package's independent table in the configured search_path.
func (i *Inbox) Migrate(ctx context.Context) error {
	_, err := i.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS factory_result_inbox_v1 (
 account TEXT NOT NULL, work_id TEXT NOT NULL, attempt_id TEXT NOT NULL,
 capability_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(capability_hash) = 32),
 expires_at TIMESTAMPTZ NOT NULL, body BYTEA,
 accepted_at TIMESTAMPTZ,
 PRIMARY KEY (account, work_id, attempt_id),
 CHECK (body IS NULL OR octet_length(body) <= 409600),
 CHECK ((body IS NULL) = (accepted_at IS NULL))
 )`)
	return storage(err)
}

func validID(s string) bool {
	return s != "" && len(s) <= 512 && utf8.ValidString(s) && !bytes.ContainsRune([]byte(s), 0)
}
func (i *Inbox) capability(account, work, attempt string) string {
	h := hmac.New(sha256.New, i.secret)
	h.Write([]byte("vmbox/resultinbox/capability/v1\x00"))
	for _, s := range []string{account, work, attempt} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func storage(err error) error {
	if err != nil {
		return ErrStorage
	}
	return nil
}

// Issue returns a pseudorandom per-attempt bearer capability. Retries recover the
// same capability, but never extend expiry. Expired attempts require a new ID.
func (i *Inbox) Issue(ctx context.Context, account, workID, attemptID string) (string, error) {
	if !validID(account) || !validID(workID) || !validID(attemptID) {
		return "", ErrInvalid
	}
	token := i.capability(account, workID, attemptID)
	hash := sha256.Sum256([]byte(token))
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return "", ErrStorage
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_result_inbox_v1
 (account,work_id,attempt_id,capability_hash,expires_at)
 VALUES ($1,$2,$3,$4,clock_timestamp()+$5 * interval '1 microsecond')
 ON CONFLICT (account,work_id,attempt_id) DO NOTHING`, account, workID, attemptID, hash[:], i.ttl.Microseconds())
	if err != nil {
		return "", ErrStorage
	}
	var stored []byte
	var expires time.Time
	err = tx.QueryRowContext(ctx, `SELECT capability_hash, expires_at
 FROM factory_result_inbox_v1 WHERE account=$1 AND work_id=$2 AND attempt_id=$3 FOR UPDATE`, account, workID, attemptID).Scan(&stored, &expires)
	if err != nil {
		return "", ErrStorage
	}
	if !hmac.Equal(stored, hash[:]) {
		return "", ErrSecretChanged
	}
	var alive bool
	if err = tx.QueryRowContext(ctx, `SELECT $1::timestamptz > clock_timestamp()`, expires).Scan(&alive); err != nil {
		return "", ErrStorage
	}
	if !alive {
		return "", ErrExpired
	}
	if err = tx.Commit(); err != nil {
		return "", ErrStorage
	}
	return token, nil
}

// Accept commits the exact callback bytes before returning success. An identical
// accepted retry succeeds even after expiry; all new results require live tokens.
func (i *Inbox) Accept(ctx context.Context, token string, body []byte) error {
	if len(body) > MaxBodyBytes {
		return ErrTooLarge
	}
	if len(token) != 43 {
		return ErrUnauthorized
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return ErrUnauthorized
	}
	report, err := Decode(body)
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(token))
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	var attempt string
	var previous []byte
	var alive bool
	err = tx.QueryRowContext(ctx, `SELECT attempt_id,body,expires_at > clock_timestamp()
 FROM factory_result_inbox_v1 WHERE capability_hash=$1 FOR UPDATE`, hash[:]).Scan(&attempt, &previous, &alive)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return ErrStorage
	}
	if report.AttemptID != attempt {
		return ErrInvalid
	}
	if previous != nil {
		if bytes.Equal(previous, body) {
			return storage(tx.Commit())
		}
		return ErrConflict
	}
	if !alive {
		return ErrExpired
	}
	// Recheck the deadline after any lock wait, using the database clock.
	result, err := tx.ExecContext(ctx, `UPDATE factory_result_inbox_v1 SET body=$2,accepted_at=clock_timestamp()
 WHERE capability_hash=$1 AND expires_at > clock_timestamp()`, hash[:], body)
	if err != nil {
		return ErrStorage
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ErrStorage
	}
	if n != 1 {
		return ErrExpired
	}
	return storage(tx.Commit())
}

// Get returns the original bytes of a persisted, unverified report. Missing
// results and foreign accounts both return ErrNotFound. Expiry does not erase results.
func (i *Inbox) Get(ctx context.Context, account, workID, attemptID string) ([]byte, error) {
	var body []byte
	err := i.db.QueryRowContext(ctx, `SELECT body FROM factory_result_inbox_v1
 WHERE account=$1 AND work_id=$2 AND attempt_id=$3 AND body IS NOT NULL`, account, workID, attemptID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, ErrStorage
	}
	return body, nil
}
