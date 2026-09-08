package execution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSchedulerLeaseRecoveryFencesOldWriter(t *testing.T) {
	s, _ := database(t)
	ctx := context.Background()
	w := approved()
	w.MaxWorkers = 2
	b, _ := json.Marshal(w)
	if _, err := s.DB.Exec(`INSERT INTO factory_work_items(id,account_id,user_id,request_key,request_hash,revision,state,document,created_at,updated_at) VALUES($1,'a','u','lease','lease',2,'build_queued',$2,now(),now())`, w.ID, b); err != nil {
		t.Fatal(err)
	}
	owner, err := s.Acquire(ctx, "a", w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Acquire(ctx, "a", w.ID); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("duplicate scheduler lease", err)
	}
	if _, err = s.Acquire(ctx, "foreign", w.ID); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("foreign lease", err)
	}
	x, err := owner.Initialize(ctx, "a", w.ID)
	if err != nil {
		t.Fatal(err)
	}
	x, err = owner.Reserve(ctx, "a", w.ID, x.Version, "api", "build", 2)
	if err != nil {
		t.Fatal(err)
	}
	attempt := x.Graph.Nodes[0].Attempt.ID
	if err = owner.Renew(ctx); err != nil {
		t.Fatal(err)
	}
	// Advance the persisted lease, not wall-clock sleeps or a mock clock.
	if _, err = s.DB.Exec(`UPDATE factory_execution_leases SET expires_at=clock_timestamp()-interval '1 second' WHERE account_id='a' AND work_id=$1`, w.ID); err != nil {
		t.Fatal(err)
	}
	if err = owner.Renew(ctx); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("expired lease revived", err)
	}
	next, err := s.Acquire(ctx, "a", w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.Release(ctx); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("old owner released successor", err)
	}
	if _, err = owner.BindBox(ctx, "a", w.ID, x.Version, "api", attempt, "builder"); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("old scheduler wrote", err)
	}
	if _, err = owner.Initialize(ctx, "a", w.ID); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("old scheduler initialized", err)
	}
	recovered, err := next.Load(ctx, "a", w.ID)
	if err != nil || recovered.Graph.Nodes[0].Attempt.ID != attempt {
		t.Fatal("recovery lost stable task intent", err)
	}
	if _, err = next.BindBox(ctx, "a", w.ID, recovered.Version, "api", attempt, "builder"); err != nil {
		t.Fatal(err)
	}
	if err = next.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err = next.Renew(ctx); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("released lease renewed", err)
	}
}
