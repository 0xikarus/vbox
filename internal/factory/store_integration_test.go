package factory

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("VMBOX_FACTORY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set VMBOX_FACTORY_TEST_DATABASE_URL to an isolated test PostgreSQL")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("test DB must use a postgres URL")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "factory_test_" + newID()
	if _, err = db.Exec("CREATE SCHEMA " + schema); err != nil {
		db.Close()
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	scoped, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scoped.Close(); db.Exec("DROP SCHEMA " + schema + " CASCADE"); db.Close() })
	s := &Store{DB: scoped}
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreDurablePlanReplyAndApproval(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	input := CreateWork{RepositoryID: "repo", Idea: "Build something", Agent: "codex", Profile: "chosen"}
	repo := Repository{ID: "repo", FullName: "owner/repo"}
	w, err := s.Create(ctx, "account", "user", "create", input, repo, "base-one", nil)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.Create(ctx, "account", "user", "create", input, repo, "base-two", nil)
	if err != nil || retry.ID != w.ID || retry.BaseSHA != "base-one" {
		t.Fatalf("retry changed immutable snapshot: %v", err)
	}
	if _, err = s.Get(ctx, "foreign", w.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign account read: %v", err)
	}
	c, err := s.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("concurrent claim accepted: %v", err)
	}
	// Simulate lost dispatcher, then ensure attempt identity survives recovery.
	if _, err = s.DB.Exec(`UPDATE factory_work_items SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, w.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Work.Attempts[0].ID != c.Work.Attempts[0].ID {
		t.Fatal("recovery invented another attempt")
	}
	if err = s.SaveClaim(ctx, c, c.Work); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale dispatcher accepted: %v", err)
	}
	p := validPlan()
	p.Questions = []string{"Should it support mobile?"}
	zero := 0
	if err = s.FinishPlan(ctx, recovered, "Actual planner response", &p, &zero, 0); err != nil {
		t.Fatal(err)
	}
	w, err = s.Get(ctx, "account", w.ID)
	if err != nil || w.State != "needs_clarification" || len(w.Messages) != 2 {
		t.Fatalf("plan did not persist: %v", err)
	}
	r := Reply{Text: "Yes, support mobile", ExpectedRevision: w.Revision}
	w, err = s.Reply(ctx, "account", w.ID, "reply", r, nil)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.Reply(ctx, "account", w.ID, "reply", r, nil)
	if err != nil || len(duplicate.Messages) != 3 || duplicate.Revision != w.Revision {
		t.Fatalf("duplicate reply appended: %v", err)
	}
	c, err = s.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p = validPlan()
	if err = s.FinishPlan(ctx, c, "Revised plan", &p, &zero, 0); err != nil {
		t.Fatal(err)
	}
	w, err = s.Get(ctx, "account", w.ID)
	if err != nil {
		t.Fatal(err)
	}
	approval := Approval{ExpectedRevision: w.Revision, PlanRevision: 2, MaxWorkers: 2}
	w, err = s.Approve(ctx, "account", w.ID, "approval", approval, 3)
	if err != nil {
		t.Fatal(err)
	}
	if w.State != "approved_queued" || w.ApprovedPlanRevision != 2 || w.MaxWorkers != 2 || len(w.Plans) != 2 {
		t.Fatal("approval or history lost")
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("migration not repeatable", err)
	}
	reopened := &Store{DB: s.DB}
	again, err := reopened.Get(ctx, "account", w.ID)
	if err != nil || len(again.Messages) != 4 {
		t.Fatalf("restart lost messages: %v", err)
	}
}

func TestStoreZeroExitIsNotAPlan(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "a", "u", "k", CreateWork{RepositoryID: "r", Idea: "idea", Agent: "claude", Profile: "p"}, Repository{ID: "r"}, "sha", nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	if err = s.FinishPlan(ctx, c, "I succeeded", nil, &zero, 0); err != nil {
		t.Fatal(err)
	}
	w, err = s.Get(ctx, "a", w.ID)
	if err != nil || w.State != "planning_failed" || len(w.Plans) != 0 {
		t.Fatalf("unstructured success accepted: %v", err)
	}
}
