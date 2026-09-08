package factory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// PlannerRunner is the trusted adapter to controller-managed box execution.
// Implementations must use Attempt.ID as the external idempotency key, stage
// verified source/images before launch, and never expose provider credentials.
type PlannerRunner interface {
	Start(context.Context, Claim) (Submission, error)
	Observe(context.Context, Claim) (Observation, error)
}
type Submission struct{ TaskID, BoxID, BoxName string }
type Observation struct {
	State     string
	Finished  bool
	ExitCode  *int
	Signal    int
	Result    []byte // Dedicated structured result, NOT a tmux or truncated log scrape.
	Truncated bool
}
type PlannerResult struct {
	Version  int    `json:"version"`
	Response string `json:"response"`
	Plan     Plan   `json:"plan"`
}

func ParsePlannerResult(data []byte) (PlannerResult, error) {
	var result PlannerResult
	if len(data) == 0 || len(data) > 300000 {
		return result, fmt.Errorf("planner result is missing or exceeds 300000 bytes")
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(&result); err != nil {
		return result, fmt.Errorf("planner result is invalid JSON")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return result, fmt.Errorf("planner returned trailing result data")
	}
	if result.Version != ContractVersion || strings.TrimSpace(result.Response) == "" || strings.TrimSpace(result.Plan.Markdown) == "" || len(result.Plan.Features) > 100 || len(result.Plan.Questions) > 50 {
		return result, fmt.Errorf("planner result has invalid version or required fields")
	}
	return result, nil
}

type Dispatcher struct {
	Store  *Store
	Runner PlannerRunner
}

// Step performs one bounded observation. It never holds a DB transaction across
// SSH or HTTP. Lease expiry can repeat Start, so its key must remain the attempt ID.
func (d *Dispatcher) Step(ctx context.Context) error {
	if d.Store == nil || d.Runner == nil {
		return fmt.Errorf("planning execution is not configured")
	}
	c, err := d.Store.Claim(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	w := c.Work
	if len(w.Attempts) == 0 {
		return fmt.Errorf("work item has no planning attempt")
	}
	a := &w.Attempts[len(w.Attempts)-1]
	if a.TaskID == "" {
		submission, err := d.Runner.Start(ctx, c)
		if err != nil {
			// An ambiguous remote response is not a failed task; retain the same
			// queued attempt so its idempotent submission can be recovered.
			w.Error = "Planning submission is awaiting confirmation; the same attempt will be reconciled."
			if saveErr := d.Store.SaveClaim(ctx, c, w); saveErr != nil {
				return errors.Join(err, saveErr)
			}
			return err
		}
		if submission.TaskID == "" || submission.BoxID == "" {
			return fmt.Errorf("planner submission lacks execution identity")
		}
		a.TaskID = submission.TaskID
		a.State = "queued"
		w.BoxID = submission.BoxID
		w.BoxName = submission.BoxName
		w.Error = ""
		return d.Store.SaveClaim(ctx, c, w)
	}
	o, err := d.Runner.Observe(ctx, c)
	if err != nil {
		w.Error = "Planning execution is temporarily unreachable; completion has not been inferred."
		if saveErr := d.Store.SaveClaim(ctx, c, w); saveErr != nil {
			return errors.Join(err, saveErr)
		}
		return err
	}
	a.State = o.State
	if !o.Finished {
		w.Error = ""
		if o.State == "unknown" {
			w.Error = "Planning execution outcome is unknown; inspect its box before retrying."
		}
		return d.Store.SaveClaim(ctx, c, w)
	}
	c.Work = w
	if o.ExitCode == nil && o.Signal == 0 {
		w.Error = "Terminal execution has no exit or signal evidence."
		return d.Store.SaveClaim(ctx, c, w)
	}
	if o.ExitCode == nil || *o.ExitCode != 0 {
		return d.Store.FinishPlan(ctx, c, "", nil, o.ExitCode, o.Signal)
	}
	if o.Truncated {
		return d.Store.FinishPlan(ctx, c, "", nil, o.ExitCode, o.Signal)
	}
	result, err := ParsePlannerResult(o.Result)
	if err != nil {
		return d.Store.FinishPlan(ctx, c, "", nil, o.ExitCode, o.Signal)
	}
	return d.Store.FinishPlan(ctx, c, result.Response, &result.Plan, o.ExitCode, o.Signal)
}

func (d *Dispatcher) Run(ctx context.Context, onError func(error)) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := d.Step(ctx); err != nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
