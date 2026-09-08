package factory

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestPlanningCapacityPersistsAcrossClaimsAndRestart(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, key := range []string{"one", "two", "three"} {
		_, err := s.Create(ctx, "a", "u", key, CreateWork{RepositoryID: "r", Idea: key, Agent: "codex", Profile: "p"}, Repository{ID: "r"}, "sha", nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.ClaimLimited(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimLimited(ctx, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("worker cap exceeded")
	}
	if _, err = s.DB.Exec(`UPDATE factory_work_items SET lease_expires_at=NULL WHERE id=$1`, first.Work.ID); err != nil {
		t.Fatal(err)
	}
	restarted := &Store{DB: s.DB}
	recovered, err := restarted.ClaimLimited(ctx, 1)
	if err != nil || recovered.Work.ID != first.Work.ID {
		t.Fatal("lease expiry admitted another worker instead of recovery")
	}
	w := recovered.Work
	w.State = "planning_failed"
	w.Attempts[0].State = "not_started"
	if err = s.SaveClaim(ctx, recovered, w); err != nil {
		t.Fatal(err)
	}
	second, err := s.ClaimLimited(ctx, 1)
	if err != nil || second.Work.ID == first.Work.ID {
		t.Fatal("terminal work did not release admission")
	}
	if _, err = s.ClaimLimited(ctx, 2); err != nil {
		t.Fatal("increased cap not honored")
	}
	if _, err = s.ClaimLimited(ctx, 7); err == nil {
		t.Fatal("more than six workers accepted")
	}
}
