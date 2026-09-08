package taskflow

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

var ErrConflict = errors.New("task version, state, or lease changed")
var ErrInvalid = errors.New("invalid task request")

type Store struct{ DB *sql.DB }

// document keeps scheduler metadata outside the root-owned public projection.
// Attempts, plans and messages are append-only; attempt observations are updated
// in place only until terminal. Revision membership survives process restarts.
type document struct {
	Workflow         Workflow          `json:"workflow"`
	Revision         int               `json:"revision"`
	AttemptRevisions map[string]int    `json:"attemptRevisions"`
	Started          map[string]bool   `json:"started,omitempty"`
	Results          map[string]Result `json:"results,omitempty"`
}

func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS general_tasks (
 account_id text NOT NULL, id text NOT NULL, user_id text NOT NULL,
 document jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease text, lease_until timestamptz, PRIMARY KEY(account_id,id));
 CREATE INDEX IF NOT EXISTS general_tasks_schedule ON general_tasks(updated_at);
 CREATE TABLE IF NOT EXISTS general_task_mutations (
 account_id text NOT NULL, request_key text NOT NULL, request_hash text NOT NULL,
 document jsonb NOT NULL, PRIMARY KEY(account_id,request_key));`)
	return err
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func fingerprint(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Store) Get(ctx context.Context, account, id string) (Workflow, error) {
	var b []byte
	err := s.DB.QueryRowContext(ctx, `SELECT document FROM general_tasks WHERE account_id=$1 AND id=$2`, account, id).Scan(&b)
	var d document
	if err == nil {
		err = json.Unmarshal(b, &d)
	}
	return d.Workflow, err
}
func (s *Store) List(ctx context.Context, account string) ([]Workflow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT document FROM general_tasks WHERE account_id=$1 ORDER BY updated_at DESC,id`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Workflow{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var d document
		if err = json.Unmarshal(b, &d); err != nil {
			return nil, err
		}
		out = append(out, d.Workflow)
	}
	return out, rows.Err()
}
func readDocument(ctx context.Context, tx *sql.Tx, account, id string) (document, error) {
	var b []byte
	err := tx.QueryRowContext(ctx, `SELECT document FROM general_tasks WHERE account_id=$1 AND id=$2 FOR UPDATE`, account, id).Scan(&b)
	var d document
	if err == nil {
		err = json.Unmarshal(b, &d)
	}
	return d, err
}
func saveDocument(ctx context.Context, tx *sql.Tx, account string, d *document) error {
	d.Workflow.Version++
	d.Workflow.UpdatedAt = time.Now().UTC()
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE general_tasks SET document=$3,updated_at=clock_timestamp() WHERE account_id=$1 AND id=$2`, account, d.Workflow.ID, b)
	return err
}

// mutate serializes idempotency before the workflow row. apply is strictly local
// state-machine code: profile, asset and runtime calls happen outside this method.
func (s *Store) mutate(ctx context.Context, account, user, key, hash, id string, version int, create *Create, apply func(*document) error) (Workflow, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Workflow{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fingerprint([]string{account, key})); err != nil {
		return Workflow{}, err
	}
	var oldHash string
	var b []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,document FROM general_task_mutations WHERE account_id=$1 AND request_key=$2`, account, key).Scan(&oldHash, &b)
	if err == nil {
		if hash != oldHash {
			return Workflow{}, ErrConflict
		}
		var w Workflow
		err = json.Unmarshal(b, &w)
		return w, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Workflow{}, err
	}
	var d document
	if create != nil {
		now := time.Now().UTC()
		d = document{Revision: 1, AttemptRevisions: map[string]int{}, Workflow: Workflow{ID: newID(), State: "planning_queued", Idea: create.Idea, Agent: create.Agent, Profile: create.Profile, AssetIDs: append([]string{}, create.AssetIDs...), MaxWorkers: create.MaxWorkers, Messages: []Message{{Role: "user", Text: create.Idea, CreatedAt: now}}, Plans: []Plan{}, Attempts: []Attempt{}, CreatedAt: now, UpdatedAt: now}}
		addAttempt(&d, "plan", "")
		b, _ = json.Marshal(d)
		_, err = tx.ExecContext(ctx, `INSERT INTO general_tasks(account_id,id,user_id,document) VALUES($1,$2,$3,$4)`, account, d.Workflow.ID, user, b)
	} else {
		d, err = readDocument(ctx, tx, account, id)
		if err == nil && d.Workflow.Version != version {
			err = ErrConflict
		}
	}
	if err != nil {
		return Workflow{}, err
	}
	if apply != nil {
		if err = apply(&d); err != nil {
			return Workflow{}, err
		}
	}
	if err = saveDocument(ctx, tx, account, &d); err != nil {
		return Workflow{}, err
	}
	b, _ = json.Marshal(d.Workflow)
	if _, err = tx.ExecContext(ctx, `INSERT INTO general_task_mutations(account_id,request_key,request_hash,document) VALUES($1,$2,$3,$4)`, account, key, hash, b); err != nil {
		return Workflow{}, err
	}
	return d.Workflow, tx.Commit()
}

func invalid(message string) error { return fmt.Errorf("%w: %s", ErrInvalid, message) }
