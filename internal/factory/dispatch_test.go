package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

type lostResponseRunner struct {
	keys []string
	fail bool
}

func (r *lostResponseRunner) Start(_ context.Context, c Claim) (Submission, error) {
	r.keys = append(r.keys, c.Work.Attempts[len(c.Work.Attempts)-1].ID)
	if !r.fail {
		r.fail = true
		return Submission{}, fmt.Errorf("accepted but response lost")
	}
	return Submission{TaskID: "existing-task", BoxID: "box"}, nil
}
func (r *lostResponseRunner) Observe(_ context.Context, c Claim) (Observation, error) {
	p := validPlan()
	b, _ := json.Marshal(PlannerResult{Version: 1, Response: "Actual result", Plan: p})
	zero := 0
	return Observation{State: "exited", Finished: true, ExitCode: &zero, Result: b}, nil
}

func TestDispatcherRecoversSameSubmission(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "a", "u", "create", CreateWork{RepositoryID: "r", Idea: "idea", Agent: "codex", Profile: "p"}, Repository{ID: "r"}, "sha", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &lostResponseRunner{}
	d := &Dispatcher{Store: s, Runner: r}
	if d.Step(ctx) == nil {
		t.Fatal("lost response not reported")
	}
	for range 2 {
		if _, err = s.DB.Exec(`UPDATE factory_work_items SET lease_expires_at=NULL WHERE id=$1`, w.ID); err != nil {
			t.Fatal(err)
		}
		if err = d.Step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.keys) != 2 || r.keys[0] != r.keys[1] {
		t.Fatal("ambiguous request got a new identity")
	}
	w, err = s.Get(ctx, "a", w.ID)
	if err != nil || w.State != "plan_ready" || w.Attempts[0].TaskID != "existing-task" {
		t.Fatalf("execution not recovered: %v", err)
	}
}

func TestPlannerResultRejectsLogTextAndMissingData(t *testing.T) {
	for _, data := range []string{"Build passed", `{"version":1,"response":"done"}`, `{"version":2,"response":"done","plan":{"markdown":"plan"}}`, `{"version":1,"response":"done","plan":{"markdown":"plan"}} trailing`} {
		if _, err := ParsePlannerResult([]byte(data)); err == nil {
			t.Fatalf("invalid result accepted: %q", data)
		}
	}
}

type provisioningRunner struct {
	starts     int
	remembered bool
}

func (r *provisioningRunner) Start(_ context.Context, c Claim) (Submission, error) {
	r.starts++
	if r.starts == 1 {
		return Submission{BoxID: "box", BoxName: "planning-box", Pending: true, State: "creation-initializing"}, nil
	}
	r.remembered = c.Work.BoxID == "box" && c.Work.Attempts[0].State == "creation-initializing"
	return Submission{BoxID: "box", TaskID: "task"}, nil
}
func (r *provisioningRunner) Observe(context.Context, Claim) (Observation, error) {
	return Observation{}, fmt.Errorf("unexpected observation")
}

func TestDispatcherPersistsProvisioningIdentity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "a", "u", "provision", CreateWork{RepositoryID: "r", Idea: "idea", Agent: "codex", Profile: "p"}, Repository{ID: "r"}, "sha", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &provisioningRunner{}
	d := &Dispatcher{Store: s, Runner: r}
	if err = d.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE factory_work_items SET lease_expires_at=NULL WHERE id=$1`, w.ID); err != nil {
		t.Fatal(err)
	}
	if err = d.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if !r.remembered {
		t.Fatal("provisioning identity or phase lost")
	}
	w, err = s.Get(ctx, "a", w.ID)
	if err != nil || w.Attempts[0].TaskID != "task" {
		t.Fatal("task not saved after provisioning")
	}
}

type rejectedRunner struct{}

func (rejectedRunner) Start(context.Context, Claim) (Submission, error) {
	return Submission{}, RejectPlanning("The source revision is unavailable.")
}
func (rejectedRunner) Observe(context.Context, Claim) (Observation, error) {
	return Observation{}, fmt.Errorf("not started")
}
func TestPermanentRejectionDoesNotInventExit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "a", "u", "reject", CreateWork{RepositoryID: "r", Idea: "idea", Agent: "codex", Profile: "p"}, Repository{ID: "r"}, "sha", nil)
	if err != nil {
		t.Fatal(err)
	}
	d := &Dispatcher{Store: s, Runner: rejectedRunner{}}
	if err = d.Step(ctx); err != nil {
		t.Fatal(err)
	}
	w, err = s.Get(ctx, "a", w.ID)
	if err != nil || w.State != "planning_failed" || w.Attempts[0].State != "not_started" || w.Attempts[0].ExitCode != nil || w.Attempts[0].TaskID != "" {
		t.Fatal("rejection fabricated process evidence")
	}
}
