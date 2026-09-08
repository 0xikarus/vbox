package factory

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrConflict = errors.New("factory revision or execution lease changed")

type AssetRef struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

type Attempt struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
	TaskID   string `json:"taskId,omitempty"`
	State    string `json:"state"`
	ExitCode *int   `json:"exitCode"`
	Signal   int    `json:"signal,omitempty"`
}

type Work struct {
	CreateWork
	ID                   string     `json:"id"`
	Revision             int        `json:"revision"`
	State                string     `json:"state"`
	RepositoryName       string     `json:"repositoryName"`
	BaseSHA              string     `json:"baseSha"`
	Assets               []AssetRef `json:"assets"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	BoxID                string     `json:"boxId,omitempty"`
	BoxName              string     `json:"boxName,omitempty"`
	Messages             []Message  `json:"messages"`
	Plans                []Plan     `json:"plans"`
	Features             []Feature  `json:"features"`
	Attempts             []Attempt  `json:"attempts"`
	ApprovedPlanRevision int        `json:"approvedPlanRevision,omitempty"`
	MaxWorkers           int        `json:"maxWorkers,omitempty"`
	Error                string     `json:"error,omitempty"`
}

// Store uses its own tables. Controller/provider state remains behind its API.
type Store struct{ DB *sql.DB }

func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS factory_work_items (
	 id TEXT PRIMARY KEY, account_id TEXT NOT NULL, user_id TEXT NOT NULL,
	 request_key TEXT NOT NULL, request_hash TEXT NOT NULL, revision BIGINT NOT NULL,
	 state TEXT NOT NULL, document JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL,
	 updated_at TIMESTAMPTZ NOT NULL, lease TEXT, lease_expires_at TIMESTAMPTZ,
	 UNIQUE(account_id,request_key));
	 CREATE INDEX IF NOT EXISTS factory_work_queue ON factory_work_items(state,updated_at);
	 CREATE TABLE IF NOT EXISTS factory_mutations (
	 account_id TEXT NOT NULL, request_key TEXT NOT NULL, request_hash TEXT NOT NULL,
	 work_id TEXT NOT NULL REFERENCES factory_work_items(id), document JSONB NOT NULL,
	 PRIMARY KEY(account_id,request_key));`)
	return err
}

func newID() string { return hex.EncodeToString(randomBytes()) }
func randomBytes() []byte {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Store) Create(ctx context.Context, account, user, key string, input CreateWork, repository Repository, sha string, assets []AssetRef) (Work, error) {
	if account == "" || user == "" || key == "" || len(key) > 200 {
		return Work{}, fmt.Errorf("account, user and idempotency key required")
	}
	if err := input.Validate(); err != nil {
		return Work{}, err
	}
	if repository.ID != input.RepositoryID || sha == "" {
		return Work{}, fmt.Errorf("resolved repository revision required")
	}
	if len(assets) != len(input.AssetIDs) {
		return Work{}, fmt.Errorf("attachment manifest mismatch")
	}
	var total int64
	for i, a := range assets {
		if a.ID != input.AssetIDs[i] || a.Size < 1 || a.Size > 10<<20 || len(a.SHA256) != 64 {
			return Work{}, fmt.Errorf("invalid attachment manifest")
		}
		total += a.Size
	}
	if total > 40<<20 {
		return Work{}, fmt.Errorf("attachment total exceeds 40 MiB")
	}
	if assets == nil {
		assets = []AssetRef{}
	}
	now := time.Now().UTC()
	id := newID()
	attempt := newID()
	w := Work{CreateWork: input, ID: id, Revision: 1, State: "planning_queued", RepositoryName: repository.FullName, BaseSHA: sha, Assets: assets, CreatedAt: now, UpdatedAt: now,
		Messages: []Message{{ID: newID(), Role: "user", Text: input.Idea, AssetIDs: input.AssetIDs, CreatedAt: now}}, Plans: []Plan{}, Features: []Feature{}, Attempts: []Attempt{{ID: attempt, Revision: 1, State: "queued"}}}
	body, err := json.Marshal(w)
	if err != nil {
		return Work{}, err
	}
	// Hash caller intent, not a newly resolved branch head: a retry recovers the
	// original immutable source snapshot even if the branch has since advanced.
	hash := digest(input)
	_, err = s.DB.ExecContext(ctx, `INSERT INTO factory_work_items(id,account_id,user_id,request_key,request_hash,revision,state,document,created_at,updated_at) VALUES($1,$2,$3,$4,$5,1,$6,$7,$8,$8) ON CONFLICT(account_id,request_key) DO NOTHING`, id, account, user, key, hash, w.State, body, now)
	if err != nil {
		return Work{}, err
	}
	var existingHash string
	err = s.DB.QueryRowContext(ctx, `SELECT request_hash,document FROM factory_work_items WHERE account_id=$1 AND request_key=$2`, account, key).Scan(&existingHash, &body)
	if err != nil {
		return Work{}, err
	}
	if existingHash != hash {
		return Work{}, ErrConflict
	}
	err = json.Unmarshal(body, &w)
	return w, err
}

func (s *Store) Get(ctx context.Context, account, id string) (Work, error) {
	var b []byte
	err := s.DB.QueryRowContext(ctx, `SELECT document FROM factory_work_items WHERE account_id=$1 AND id=$2`, account, id).Scan(&b)
	if err != nil {
		return Work{}, err
	}
	var w Work
	err = json.Unmarshal(b, &w)
	return w, err
}

func (s *Store) List(ctx context.Context, account string) ([]Work, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT document FROM factory_work_items WHERE account_id=$1 ORDER BY created_at DESC,id DESC LIMIT 100`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Work{}
	for rows.Next() {
		var b []byte
		var w Work
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &w); err != nil {
			return nil, err
		}
		w.Messages = nil
		w.Plans = nil
		out = append(out, w)
	}
	return out, rows.Err()
}

type Claim struct {
	AccountID string
	Lease     string
	Work      Work
}

// Claim recovers the SAME attempt after lease expiry. A dispatcher must use its
// persisted attempt ID as the controller submission key, never launch a new one.
func (s *Store) Claim(ctx context.Context) (Claim, error) {
	return s.ClaimLimited(ctx, 0)
}

// ClaimLimited caps admitted planning work across coordinator processes sharing
// this store. Already-admitted work remains observable at the cap and after
// lowering it; a lease expiry does not release its compute reservation.
func (s *Store) ClaimLimited(ctx context.Context, limit int) (Claim, error) {
	if limit < 0 || limit > 6 {
		return Claim{}, fmt.Errorf("invalid planning worker limit")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Claim{}, err
	}
	defer tx.Rollback()
	if limit > 0 {
		var locked bool
		if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(1986880102,1)`).Scan(&locked); err != nil {
			return Claim{}, err
		}
		if !locked {
			return Claim{}, sql.ErrNoRows
		}
	}
	var c Claim
	var b []byte
	err = tx.QueryRowContext(ctx, `SELECT account_id,document FROM factory_work_items WHERE
 (state='planning' OR (state='planning_queued' AND ($1=0 OR (SELECT count(*) FROM factory_work_items WHERE state='planning')<$1)))
 AND (lease_expires_at IS NULL OR lease_expires_at<now()) ORDER BY updated_at,id FOR UPDATE SKIP LOCKED LIMIT 1`, limit).Scan(&c.AccountID, &b)
	if err != nil {
		return Claim{}, err
	}
	if err = json.Unmarshal(b, &c.Work); err != nil {
		return Claim{}, err
	}
	c.Lease = newID()
	c.Work.State = "planning"
	c.Work.UpdatedAt = time.Now().UTC()
	b, _ = json.Marshal(c.Work)
	_, err = tx.ExecContext(ctx, `UPDATE factory_work_items SET state='planning',document=$3,lease=$4,lease_expires_at=now()+interval '60 seconds',updated_at=now() WHERE account_id=$1 AND id=$2`, c.AccountID, c.Work.ID, b, c.Lease)
	if err != nil {
		return Claim{}, err
	}
	err = tx.Commit()
	return c, err
}

// SaveClaim rejects late results after another dispatcher reclaimed the item.
// Network operations occur outside DB transactions; short polling releases its
// lease between observations instead of blocking the queue on a long agent turn.
func (s *Store) SaveClaim(ctx context.Context, c Claim, w Work) error {
	if w.ID != c.Work.ID || w.Revision != c.Work.Revision {
		return ErrConflict
	}
	w.UpdatedAt = time.Now().UTC()
	b, err := json.Marshal(w)
	if err != nil {
		return err
	}
	r, err := s.DB.ExecContext(ctx, `UPDATE factory_work_items SET document=$4,state=$5,updated_at=now(),lease=NULL,lease_expires_at=now()+interval '5 seconds' WHERE account_id=$1 AND id=$2 AND lease=$3 AND lease_expires_at>now() AND revision=$6`, c.AccountID, w.ID, c.Lease, b, w.State, w.Revision)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}

// RenewClaim only extends the current unexpired lease. It cannot resurrect a
// stale dispatcher or regain a claim invalidated by a revision change.
func (s *Store) RenewClaim(ctx context.Context, c Claim) error {
	r, err := s.DB.ExecContext(ctx, `UPDATE factory_work_items SET lease_expires_at=now()+interval '60 seconds'
 WHERE account_id=$1 AND id=$2 AND lease=$3 AND lease_expires_at>now() AND revision=$4 AND state='planning'`, c.AccountID, c.Work.ID, c.Lease, c.Work.Revision)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}
