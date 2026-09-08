package execution

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
)

var ErrLeaseLost = errors.New("execution scheduler lease unavailable or expired")

// Lease owns coordination, not the remote process. Expiry permits inspection and
// recovery by another scheduler; it never authorizes replaying a submitted task.
type Lease struct {
	AccountID string
	WorkID    string
	token     string
}

// Acquire grants sixty seconds of coordination for one published work item.
// Use the returned Store for every mutation and Renew during bounded network
// operations. SQL transactions are never held open across those operations.
func (s Store) Acquire(ctx context.Context, account, workID string) (Store, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Store{}, err
	}
	l := &Lease{AccountID: account, WorkID: workID, token: hex.EncodeToString(random[:])}
	var token string
	err := s.DB.QueryRowContext(ctx, `INSERT INTO factory_execution_leases(account_id,work_id,token,expires_at)
SELECT account_id,id,$3,clock_timestamp()+interval '60 seconds' FROM factory_work_items
WHERE account_id=$1 AND id=$2 AND state IN ('build_queued','implementing')
ON CONFLICT(account_id,work_id) DO UPDATE SET token=EXCLUDED.token, expires_at=EXCLUDED.expires_at
WHERE factory_execution_leases.expires_at<=clock_timestamp()
RETURNING token`, account, workID, l.token).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return Store{}, ErrLeaseLost
	}
	if err != nil {
		return Store{}, err
	}
	return Store{DB: s.DB, lease: l}, nil
}

func (s Store) Renew(ctx context.Context) error {
	if s.lease == nil {
		return ErrLeaseLost
	}
	l := s.lease
	r, err := s.DB.ExecContext(ctx, `UPDATE factory_execution_leases SET expires_at=clock_timestamp()+interval '60 seconds'
WHERE account_id=$1 AND work_id=$2 AND token=$3 AND expires_at>clock_timestamp()`, l.AccountID, l.WorkID, l.token)
	return leaseChanged(r, err)
}

func (s Store) Release(ctx context.Context) error {
	if s.lease == nil {
		return ErrLeaseLost
	}
	l := s.lease
	r, err := s.DB.ExecContext(ctx, `DELETE FROM factory_execution_leases WHERE account_id=$1 AND work_id=$2 AND token=$3`, l.AccountID, l.WorkID, l.token)
	return leaseChanged(r, err)
}

func leaseChanged(r sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s Store) guardLease(ctx context.Context, tx *sql.Tx, account, workID string) error {
	// Unleased stores remain available for direct trusted operations/tests. The
	// background scheduler must exclusively use the Store returned by Acquire.
	if s.lease == nil {
		return nil
	}
	l := s.lease
	if l.AccountID != account || l.WorkID != workID {
		return ErrLeaseLost
	}
	var token string
	err := tx.QueryRowContext(ctx, `SELECT token FROM factory_execution_leases
WHERE account_id=$1 AND work_id=$2 AND token=$3 AND expires_at>clock_timestamp() FOR UPDATE`, account, workID, l.token).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	return err
}
