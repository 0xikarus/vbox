package factory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Reply struct {
	Text             string   `json:"text"`
	AssetIDs         []string `json:"assetIds"`
	ExpectedRevision int      `json:"expectedRevision"`
}

type Approval struct {
	ExpectedRevision int `json:"expectedRevision"`
	PlanRevision     int `json:"planRevision"`
	MaxWorkers       int `json:"maxWorkers"`
}

func (s *Store) mutate(ctx context.Context, account, id, key, hash string, expected int, change func(*Work) error) (Work, error) {
	if account == "" || id == "" || key == "" || len(key) > 200 {
		return Work{}, fmt.Errorf("work identity and idempotency key required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, err
	}
	defer tx.Rollback()
	var data []byte
	err = tx.QueryRowContext(ctx, `SELECT document FROM factory_work_items WHERE account_id=$1 AND id=$2 FOR UPDATE`, account, id).Scan(&data)
	if err != nil {
		return Work{}, err
	}
	var priorHash, priorID string
	var previous []byte
	err = tx.QueryRowContext(ctx, `SELECT work_id,request_hash,document FROM factory_mutations WHERE account_id=$1 AND request_key=$2`, account, key).Scan(&priorID, &priorHash, &previous)
	if err == nil {
		if priorID != id || priorHash != hash {
			return Work{}, ErrConflict
		}
		var old Work
		err = json.Unmarshal(previous, &old)
		return old, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Work{}, err
	}
	var w Work
	if err = json.Unmarshal(data, &w); err != nil {
		return Work{}, err
	}
	if w.Revision != expected {
		return Work{}, ErrConflict
	}
	if err = change(&w); err != nil {
		return Work{}, err
	}
	w.Revision++
	w.UpdatedAt = time.Now().UTC()
	data, err = json.Marshal(w)
	if err != nil {
		return Work{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE factory_work_items SET document=$3,revision=$4,state=$5,updated_at=now(),lease=NULL,lease_expires_at=NULL WHERE account_id=$1 AND id=$2`, account, id, data, w.Revision, w.State)
	if err != nil {
		return Work{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_mutations(account_id,request_key,request_hash,work_id,document) VALUES($1,$2,$3,$4,$5)`, account, key, hash, id, data)
	if err != nil {
		return Work{}, err
	}
	err = tx.Commit()
	return w, err
}

func (s *Store) Reply(ctx context.Context, account, id, key string, r Reply, assets []AssetRef) (Work, error) {
	if strings.TrimSpace(r.Text) == "" || len(r.Text) > 100000 || len(r.AssetIDs) != len(assets) {
		return Work{}, fmt.Errorf("reply text and validated attachment manifest required")
	}
	return s.mutate(ctx, account, id, key, digest(r), r.ExpectedRevision, func(w *Work) error {
		if w.State != "plan_ready" && w.State != "needs_clarification" && w.State != "planning_failed" {
			return ErrConflict
		}
		if len(w.Messages) >= 100 {
			return fmt.Errorf("conversation limit reached; start a new work item")
		}
		known := map[string]bool{}
		for _, a := range w.Assets {
			known[a.ID] = true
		}
		for i, a := range assets {
			if a.ID != r.AssetIDs[i] || a.Size < 1 || a.Size > 10<<20 || len(a.SHA256) != 64 {
				return fmt.Errorf("invalid reply asset")
			}
			if !known[a.ID] {
				w.Assets = append(w.Assets, a)
				w.AssetIDs = append(w.AssetIDs, a.ID)
				known[a.ID] = true
			}
		}
		var size int64
		for _, a := range w.Assets {
			size += a.Size
		}
		if len(w.Assets) > 8 || size > 40<<20 {
			return fmt.Errorf("conversation attachment limit reached")
		}
		now := time.Now().UTC()
		w.Messages = append(w.Messages, Message{ID: newID(), Role: "user", Text: r.Text, AssetIDs: r.AssetIDs, CreatedAt: now})
		w.Attempts = append(w.Attempts, Attempt{ID: newID(), Revision: w.Revision + 1, State: "queued"})
		w.State = "planning_queued"
		w.Error = ""
		return nil
	})
}

func (s *Store) Approve(ctx context.Context, account, id, key string, r Approval, workerLimit int) (Work, error) {
	if r.MaxWorkers < 1 || r.MaxWorkers > workerLimit {
		return Work{}, fmt.Errorf("worker count exceeds allowed capacity")
	}
	return s.mutate(ctx, account, id, key, digest(r), r.ExpectedRevision, func(w *Work) error {
		if w.State != "plan_ready" || len(w.Plans) == 0 {
			return ErrConflict
		}
		p := w.Plans[len(w.Plans)-1]
		if p.Revision != r.PlanRevision || p.BaseSHA != w.BaseSHA {
			return ErrConflict
		}
		if err := p.ValidateApproval(); err != nil {
			return err
		}
		// Durable state is an outbox signal, not proof of GitHub issue creation.
		w.State = "approved_queued"
		w.Features = append([]Feature{}, p.Features...)
		w.ApprovedPlanRevision = p.Revision
		w.MaxWorkers = r.MaxWorkers
		return nil
	})
}

// FinishPlan accepts a parsed agent result only after its actual process exit.
// The supplied Plan is never invented from terminal activity or a successful exit.
func (s *Store) FinishPlan(ctx context.Context, c Claim, text string, plan *Plan, exitCode *int, signal int) error {
	w := c.Work
	if len(w.Attempts) == 0 {
		return fmt.Errorf("planning attempt missing")
	}
	a := &w.Attempts[len(w.Attempts)-1]
	a.ExitCode = exitCode
	a.Signal = signal
	if exitCode == nil && signal == 0 {
		return fmt.Errorf("planning execution outcome remains unknown")
	}
	if exitCode == nil || *exitCode != 0 {
		a.State = "exited"
		w.State = "planning_failed"
		w.Error = "Planning process did not succeed; inspect its execution evidence."
		return s.SaveClaim(ctx, c, w)
	}
	a.State = "exited"
	if strings.TrimSpace(text) == "" || len(text) > 200000 || plan == nil || strings.TrimSpace(plan.Markdown) == "" {
		w.State = "planning_failed"
		w.Error = "Planner finished without a valid structured plan."
		return s.SaveClaim(ctx, c, w)
	}
	plan.Revision = len(w.Plans) + 1
	plan.AttemptID = a.ID
	plan.BaseSHA = w.BaseSHA
	plan.CreatedAt = time.Now().UTC()
	w.Messages = append(w.Messages, Message{ID: newID(), Role: "assistant", Text: text, AssetIDs: []string{}, CreatedAt: time.Now().UTC(), AttemptID: a.ID})
	w.Plans = append(w.Plans, *plan)
	w.State = "plan_ready"
	if len(plan.Questions) > 0 {
		w.State = "needs_clarification"
	}
	w.Error = ""
	return s.SaveClaim(ctx, c, w)
}
