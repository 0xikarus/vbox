package taskflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type claim struct {
	account, id, token, attempt string
	doc                         document
}

// ActiveCountSQL is the reciprocal admission query that existing Factory
// planning/execution schedulers must add under lock (1986880102,1).
const ActiveCountSQL = `SELECT count(*) FROM general_tasks g, jsonb_array_elements(g.document->'workflow'->'attempts') a WHERE a->>'state' IN ('provisioning','submitted','running')`

// ActiveCount also works before general-task storage has been enabled.
// Admission callers must hold the shared capacity advisory lock.
func ActiveCount(ctx context.Context, tx *sql.Tx) (int, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('general_tasks') IS NOT NULL`).Scan(&exists); err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	var count int
	err := tx.QueryRowContext(ctx, ActiveCountSQL).Scan(&count)
	return count, err
}

func nextActive(d *document) string {
	start := 0
	for i, a := range d.Workflow.Attempts {
		if a.ID == d.LastPolled {
			start = i + 1
			break
		}
	}
	for offset := range d.Workflow.Attempts {
		a := d.Workflow.Attempts[(start+offset)%len(d.Workflow.Attempts)]
		if active(a) {
			return a.ID
		}
	}
	return ""
}

func capacity(ctx context.Context, tx *sql.Tx) (int, error) {
	var total int
	if err := tx.QueryRowContext(ctx, ActiveCountSQL).Scan(&total); err != nil {
		return 0, err
	}
	for _, q := range []struct{ table, query string }{
		{"factory_work_items", `SELECT count(*) FROM factory_work_items WHERE state='planning'`},
		{"factory_execution_graphs", `SELECT count(*) FROM factory_execution_graphs e, jsonb_array_elements(e.document->'nodes') n WHERE n->>'state' IN ('building','verifying','reviewing')`},
	} {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, q.table).Scan(&exists); err != nil {
			return 0, err
		}
		if exists {
			var count int
			if err := tx.QueryRowContext(ctx, q.query).Scan(&count); err != nil {
				return 0, err
			}
			total += count
		}
	}
	return total, nil
}
func (s *Service) claim(ctx context.Context) (claim, error) {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return claim{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1986880102,1)`); err != nil {
		return claim{}, err
	}
	count, err := capacity(ctx, tx)
	if err != nil {
		return claim{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT account_id,id,document FROM general_tasks WHERE (lease_until IS NULL OR lease_until<=clock_timestamp()) AND document->'workflow'->>'state' IN ('planning_queued','planning','running','synthesizing','cancelling') ORDER BY updated_at,id FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return claim{}, err
	}
	var candidates []claim
	for rows.Next() {
		var c claim
		var b []byte
		if err = rows.Scan(&c.account, &c.id, &b); err != nil {
			break
		}
		if err = json.Unmarshal(b, &c.doc); err != nil {
			break
		}
		candidates = append(candidates, c)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return claim{}, err
	}
	if rowErr != nil {
		return claim{}, rowErr
	}
	for _, c := range candidates {
		d := &c.doc
		advance(d)
		current := 0
		for _, a := range d.Workflow.Attempts {
			if active(a) {
				current++
			}
		}
		// Admit a dependency-ready worker before polling when there is capacity.
		// Only one external call is made by each Step, allowing bounded multiplexing.
		if count < s.limit() && current < d.Workflow.MaxWorkers {
			for i := range d.Workflow.Attempts {
				a := &d.Workflow.Attempts[i]
				if ready(d, *a) {
					a.State = "provisioning"
					c.attempt = a.ID
					if a.Stage == "plan" {
						d.Workflow.State = "planning"
					}
					break
				}
			}
		}
		if c.attempt == "" {
			c.attempt = nextActive(d)
		}
		if c.attempt == "" {
			if d.Workflow.State == "cancelled" {
				if err = saveDocument(ctx, tx, c.account, d); err != nil {
					return claim{}, err
				}
			}
			continue
		}
		c.token = newID()
		d.LastPolled = c.attempt
		if err = saveDocument(ctx, tx, c.account, d); err != nil {
			return claim{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE general_tasks SET lease=$3,lease_until=clock_timestamp()+interval '60 seconds' WHERE account_id=$1 AND id=$2`, c.account, c.id, c.token); err != nil {
			return claim{}, err
		}
		return c, tx.Commit()
	}
	return claim{}, tx.Commit()
}

// leased merges each observation with the latest user mutation, so cancellation
// cannot be overwritten by a stale scheduler snapshot. No external callbacks.
func (s *Service) leased(ctx context.Context, c claim, apply func(*document) error) (document, error) {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return document{}, err
	}
	defer tx.Rollback()
	var token string
	err = tx.QueryRowContext(ctx, `SELECT lease FROM general_tasks WHERE account_id=$1 AND id=$2 AND lease=$3 AND lease_until>clock_timestamp() FOR UPDATE`, c.account, c.id, c.token).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return document{}, ErrConflict
	}
	if err != nil {
		return document{}, err
	}
	d, err := readDocument(ctx, tx, c.account, c.id)
	if err != nil {
		return d, err
	}
	if err = apply(&d); err != nil {
		return d, err
	}
	if err = saveDocument(ctx, tx, c.account, &d); err != nil {
		return d, err
	}
	return d, tx.Commit()
}
func attemptByID(d *document, id string) *Attempt {
	for i := range d.Workflow.Attempts {
		if d.Workflow.Attempts[i].ID == id {
			return &d.Workflow.Attempts[i]
		}
	}
	return nil
}

// Step performs one bounded scheduling operation. Runner.Start must recover by
// attempt ID before submitting; an error or timeout never creates a new attempt.
// Poll Step continuously (e.g. every second); multiple processes are supported.
func (s *Service) Step(ctx context.Context) error {
	if s.Store == nil || s.Runner == nil {
		return fmt.Errorf("task store and runner required")
	}
	c, err := s.claim(ctx)
	if err != nil || c.attempt == "" {
		return err
	}
	runCtx, cancelRun := context.WithTimeout(ctx, 45*time.Second)
	done := make(chan struct{})
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-runCtx.Done():
				return
			case <-ticker.C:
				renewalCtx, stop := context.WithTimeout(runCtx, 10*time.Second)
				r, e := s.Store.DB.ExecContext(renewalCtx, `UPDATE general_tasks SET lease_until=clock_timestamp()+interval '60 seconds' WHERE account_id=$1 AND id=$2 AND lease=$3 AND lease_until>clock_timestamp()`, c.account, c.id, c.token)
				stop()
				if e != nil {
					cancelRun()
					return
				}
				n, e := r.RowsAffected()
				if e != nil || n != 1 {
					cancelRun()
					return
				}
			}
		}
	}()
	defer func() {
		close(done)
		cancelRun()
		<-renewed
		releaseCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = s.Store.DB.ExecContext(releaseCtx, `UPDATE general_tasks SET lease=NULL,lease_until=NULL WHERE account_id=$1 AND id=$2 AND lease=$3`, c.account, c.id, c.token)
	}()
	a := attemptByID(&c.doc, c.attempt)
	if a.State == "provisioning" {
		if !c.doc.Started[a.ID] {
			validationErr := s.validateSelection(runCtx, c.account, selection(c.doc.Workflow))
			d, e := s.leased(runCtx, c, func(d *document) error {
				a := attemptByID(d, c.attempt)
				if d.Workflow.State == "cancelling" {
					a.State = "result_missing"
					a.Failure = "cancelled before dispatch"
					advance(d)
					return nil
				}
				if validationErr != nil {
					return acceptObservation(d, a.ID, Observation{Finished: true, Failure: "dispatch validation: " + validationErr.Error()})
				}
				if d.Started == nil {
					d.Started = map[string]bool{}
				}
				d.Started[a.ID] = true
				return nil
			})
			if e != nil {
				return e
			}
			c.doc = d
			a = attemptByID(&c.doc, c.attempt)
			if terminal(*a) {
				return nil
			}
		}
		// Recheck cancellation before recovering a submission. A cancelling task
		// can only be observed, including when the submission receipt was lost.
		d, e := s.leased(runCtx, c, func(*document) error { return nil })
		if e != nil {
			return e
		}
		c.doc = d
		a = attemptByID(&c.doc, c.attempt)
		if d.Workflow.State == "cancelling" {
			return s.observe(runCtx, c)
		}
		submission, startErr := s.Runner.Start(runCtx, Input{AccountID: c.account, Workflow: c.doc.Workflow, Attempt: *a})
		_, err = s.leased(runCtx, c, func(d *document) error {
			a := attemptByID(d, c.attempt)
			if startErr != nil {
				a.Failure = "submission uncertain: " + startErr.Error()
				return nil
			}
			if submission.BoxID != "" {
				if a.BoxID != "" && a.BoxID != submission.BoxID {
					return ErrConflict
				}
				a.BoxID = submission.BoxID
			}
			if submission.TaskID != "" {
				if a.TaskID != "" && a.TaskID != submission.TaskID {
					return ErrConflict
				}
				a.TaskID = submission.TaskID
			}
			a.Failure = ""
			if !submission.Pending && a.BoxID != "" && a.TaskID != "" {
				a.State = "submitted"
			} else if !submission.Pending {
				a.Failure = "submission receipt missing; recovering same attempt"
			}
			return nil
		})
		if err != nil {
			return err
		}
		return startErr
	}
	return s.observe(runCtx, c)
}

func (s *Service) observe(ctx context.Context, c claim) error {
	a := attemptByID(&c.doc, c.attempt)
	o, observeErr := s.Runner.Observe(ctx, Input{AccountID: c.account, Workflow: c.doc.Workflow, Attempt: *a})
	_, err := s.leased(ctx, c, func(d *document) error {
		if observeErr != nil {
			attemptByID(d, c.attempt).Failure = "observation unavailable: " + observeErr.Error()
			return nil
		}
		return acceptObservation(d, c.attempt, o)
	})
	if err != nil {
		return err
	}
	return observeErr
}
