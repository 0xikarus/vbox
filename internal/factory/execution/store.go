package execution

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/taskflow"
)

var ErrConflict = errors.New("feature execution revision or identity changed")
var ErrCapacity = errors.New("feature worker capacity is occupied")

type Store struct {
	DB    *sql.DB
	lease *Lease
}
type Snapshot struct {
	Version int
	Graph   Graph
}

func (s Store) Migrate(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS factory_execution_graphs (
 account_id TEXT NOT NULL, work_id TEXT NOT NULL REFERENCES factory_work_items(id),
 version BIGINT NOT NULL, document JSONB NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(account_id,work_id));
CREATE TABLE IF NOT EXISTS factory_execution_leases (
 account_id TEXT NOT NULL, work_id TEXT NOT NULL REFERENCES factory_work_items(id),
 token TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(account_id,work_id))`)
	return err
}
func (s Store) Initialize(ctx context.Context, account, workID string) (Snapshot, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback()
	if err = s.guardLease(ctx, tx, account, workID); err != nil {
		return Snapshot{}, err
	}
	var raw, stored []byte
	var previous Snapshot
	if err = tx.QueryRowContext(ctx, `SELECT document FROM factory_work_items WHERE account_id=$1 AND id=$2 FOR UPDATE`, account, workID).Scan(&raw); err != nil {
		return Snapshot{}, err
	}
	err = tx.QueryRowContext(ctx, `SELECT version,document FROM factory_execution_graphs WHERE account_id=$1 AND work_id=$2`, account, workID).Scan(&previous.Version, &stored)
	if err == nil {
		if err = json.Unmarshal(stored, &previous.Graph); err != nil {
			return Snapshot{}, err
		}
		return previous, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, err
	}
	var w factory.Work
	if err = json.Unmarshal(raw, &w); err != nil {
		return Snapshot{}, err
	}
	g, err := New(w)
	if err != nil {
		return Snapshot{}, err
	}
	b, _ := json.Marshal(g)
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_execution_graphs(account_id,work_id,version,document) VALUES($1,$2,1,$3)`, account, workID, b); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Version: 1, Graph: g}, tx.Commit()
}
func (s Store) Load(ctx context.Context, account, workID string) (Snapshot, error) {
	var v Snapshot
	var b []byte
	err := s.DB.QueryRowContext(ctx, `SELECT version,document FROM factory_execution_graphs WHERE account_id=$1 AND work_id=$2`, account, workID).Scan(&v.Version, &b)
	if err == nil {
		err = json.Unmarshal(b, &v.Graph)
	}
	return v, err
}

// Reserve persists stage intent before any network call. A stable stage ID is
// recovered from Load after a crash; a timeout never creates another attempt.
func (s Store) Reserve(ctx context.Context, account, workID string, version int, featureID, stage string, limit int) (Snapshot, error) {
	if limit < 1 || limit > 6 {
		return Snapshot{}, fmt.Errorf("worker limit must be 1–6")
	}
	return s.mutate(ctx, account, workID, version, func(tx *sql.Tx, w *factory.Work, g *Graph) error {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM factory_work_items WHERE state='planning')+
 (SELECT count(*) FROM factory_execution_graphs e, jsonb_array_elements(e.document->'nodes') n WHERE n->>'state' IN ('building','verifying','reviewing'))`).Scan(&active); err != nil {
			return err
		}
		taskWorkers, countErr := taskflow.ActiveCount(ctx, tx)
		if countErr != nil {
			return countErr
		}
		active += taskWorkers
		local := 0
		for _, n := range g.Nodes {
			if n.State == "building" || n.State == "verifying" || n.State == "reviewing" {
				local++
			}
		}
		if active >= limit || w.MaxWorkers < 1 || local >= w.MaxWorkers {
			return ErrCapacity
		}
		n, err := g.node(featureID)
		if err != nil {
			return err
		}
		switch stage {
		case "build":
			if err = g.Begin(featureID); err != nil {
				return err
			}
		case "verify":
			if n.State != "needs_verification" {
				return ErrConflict
			}
			n.State = "verifying"
		case "review":
			if n.State != "needs_review" {
				return ErrConflict
			}
			n.State = "reviewing"
		default:
			return fmt.Errorf("invalid feature execution stage")
		}
		h := sha256.Sum256([]byte(g.WorkID + "\x00" + n.Branch + "\x00" + stage))
		n.Attempt = &StageAttempt{ID: hex.EncodeToString(h[:16]), Stage: stage, State: "queued"}
		return nil
	})
}

// BindBox durably records provisioning before a process exists. It leaves the
// attempt queued and never fabricates a task or exit. Subsequent submission must
// use this same box; the version fence rejects competing provisioning writers.
func (s Store) BindBox(ctx context.Context, account, workID string, version int, id, attemptID, boxID string) (Snapshot, error) {
	return s.mutate(ctx, account, workID, version, func(_ *sql.Tx, _ *factory.Work, g *Graph) error {
		n, err := g.node(id)
		if err != nil {
			return err
		}
		if n.Attempt == nil || n.Attempt.ID != attemptID || boxID == "" || n.Attempt.State != "queued" || n.Attempt.TaskID != "" || (n.Attempt.BoxID != "" && n.Attempt.BoxID != boxID) {
			return ErrConflict
		}
		n.Attempt.BoxID = boxID
		return nil
	})
}

func (s Store) Bind(ctx context.Context, account, workID string, version int, id, attemptID, boxID, taskID string) (Snapshot, error) {
	return s.mutate(ctx, account, workID, version, func(_ *sql.Tx, _ *factory.Work, g *Graph) error {
		n, err := g.node(id)
		if err != nil {
			return err
		}
		if n.Attempt == nil || n.Attempt.ID != attemptID || boxID == "" || taskID == "" || (n.Attempt.State != "queued" && n.Attempt.State != "submitted") {
			return ErrConflict
		}
		if (n.Attempt.BoxID != "" && n.Attempt.BoxID != boxID) || (n.Attempt.TaskID != "" && n.Attempt.TaskID != taskID) {
			return ErrConflict
		}
		n.Attempt.BoxID = boxID
		n.Attempt.TaskID = taskID
		n.Attempt.State = "submitted"
		return nil
	})
}
func (s Store) AcceptBuild(ctx context.Context, a, w string, v int, id string, b Build) (Snapshot, error) {
	return s.accept(ctx, a, w, v, id, "build", b.Process, func(g *Graph) error { return g.Built(id, b) })
}
func (s Store) AcceptVerification(ctx context.Context, a, w string, v int, id string, r Verification) (Snapshot, error) {
	return s.accept(ctx, a, w, v, id, "verify", r.Process, func(g *Graph) error { return g.Verified(id, r) })
}
func (s Store) AcceptReview(ctx context.Context, a, w string, v int, id string, r Review) (Snapshot, error) {
	return s.accept(ctx, a, w, v, id, "review", r.Process, func(g *Graph) error { return g.Reviewed(id, r) })
}

// RejectReview preserves a valid negative independent review, including its
// real exit and findings. It does not convert that decision into a process error.
func (s Store) RejectReview(ctx context.Context, a, w string, v int, id string, r Review) (Snapshot, error) {
	return s.accept(ctx, a, w, v, id, "review", r.Process, func(g *Graph) error {
		n, err := g.node(id)
		if err != nil || n.State != "reviewing" || n.Build == nil || r.Approved || !success(r.Process) || r.BoxID == n.Build.BoxID || r.CandidateSHA != n.Build.CandidateSHA || strings.TrimSpace(r.Summary) == "" {
			return ErrConflict
		}
		n.Review = &r
		n.State = "review_failed"
		n.Attempt.Failure = "review_rejected"
		return nil
	})
}

// Fail records an observed terminal process separately from semantic rejection.
// An observation timeout or SSH failure with no actual exit is not terminal.
// Reasons are coordinator-owned codes, never raw agent/provider diagnostics.
func (s Store) Fail(ctx context.Context, a, w string, v int, id, stage string, p Process, reason string) (Snapshot, error) {
	if (p.ExitCode == nil && (p.Signal < 1 || p.Signal > 64)) ||
		(p.ExitCode != nil && (*p.ExitCode < 0 || *p.ExitCode > 255 || p.Signal != 0)) {
		return Snapshot{}, ErrConflict
	}
	switch reason {
	case "process_failed":
		if p.ExitCode != nil && *p.ExitCode == 0 {
			return Snapshot{}, ErrConflict
		}
	case "candidate_rejected", "checks_rejected", "review_rejected", "result_missing":
	default:
		return Snapshot{}, ErrConflict
	}
	return s.accept(ctx, a, w, v, id, stage, p, func(g *Graph) error {
		n, err := g.node(id)
		if err != nil {
			return err
		}
		if (stage != "build" || n.State != "building") &&
			(stage != "verify" || n.State != "verifying") &&
			(stage != "review" || n.State != "reviewing") {
			return ErrConflict
		}
		n.State = stage + "_failed"
		n.Attempt.Failure = reason
		return nil
	})
}

// Publish is called only with an independently reconciled GitHub PR result.
// The graph continues to distinguish PR readiness from integration acceptance.
func (s Store) Publish(ctx context.Context, a, w string, v int, id, sha, pr string) (Snapshot, error) {
	return s.mutate(ctx, a, w, v, func(_ *sql.Tx, _ *factory.Work, g *Graph) error {
		return g.Published(id, sha, pr)
	})
}
func (s Store) accept(ctx context.Context, a, w string, v int, id, stage string, p Process, apply func(*Graph) error) (Snapshot, error) {
	return s.mutate(ctx, a, w, v, func(_ *sql.Tx, _ *factory.Work, g *Graph) error {
		n, err := g.node(id)
		if err != nil {
			return err
		}
		if n.Attempt == nil || n.Attempt.State != "submitted" || n.Attempt.Stage != stage || n.Attempt.ID != p.AttemptID || n.Attempt.TaskID == "" || n.Attempt.TaskID != p.TaskID || n.Attempt.BoxID != p.BoxID {
			return ErrConflict
		}
		if err = apply(g); err != nil {
			return err
		}
		n.Attempt.State = "exited"
		n.Attempt.Process = &p
		return nil
	})
}

// Graph and UI projection change in one transaction, against the same approved
// source/plan. All admission paths acquire the global lock before work row locks.
func (s Store) mutate(ctx context.Context, account, workID string, version int, apply func(*sql.Tx, *factory.Work, *Graph) error) (Snapshot, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1986880102,1)`); err != nil {
		return Snapshot{}, err
	}
	if err = s.guardLease(ctx, tx, account, workID); err != nil {
		return Snapshot{}, err
	}
	var workJSON, graphJSON []byte
	var current int
	if err = tx.QueryRowContext(ctx, `SELECT document FROM factory_work_items WHERE account_id=$1 AND id=$2 FOR UPDATE`, account, workID).Scan(&workJSON); err != nil {
		return Snapshot{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT version,document FROM factory_execution_graphs WHERE account_id=$1 AND work_id=$2 FOR UPDATE`, account, workID).Scan(&current, &graphJSON); err != nil {
		return Snapshot{}, err
	}
	if current != version {
		return Snapshot{}, ErrConflict
	}
	var w factory.Work
	var g Graph
	if json.Unmarshal(workJSON, &w) != nil || json.Unmarshal(graphJSON, &g) != nil {
		return Snapshot{}, fmt.Errorf("invalid persisted execution")
	}
	if w.ApprovedPlanRevision != g.PlanRevision || w.BaseSHA != g.BaseSHA || (w.State != "build_queued" && w.State != "implementing") {
		return Snapshot{}, ErrConflict
	}
	if err = apply(tx, &w, &g); err != nil {
		return Snapshot{}, err
	}
	for i := range w.Features {
		n, e := g.node(w.Features[i].ID)
		if e != nil {
			return Snapshot{}, ErrConflict
		}
		w.Features[i].State = n.State
		w.Features[i].PRURL = n.PRURL
		if n.Attempt != nil {
			w.Features[i].BoxID = n.Attempt.BoxID
			a := n.Attempt
			projected := factory.FeatureAttempt{ID: a.ID, FeatureID: n.Feature.ID, Stage: a.Stage, State: a.State, BoxID: a.BoxID, TaskID: a.TaskID, Failure: a.Failure}
			if a.Stage == "review" && n.Review != nil {
				projected.Summary = n.Review.Summary
				projected.Findings = append([]string{}, n.Review.BlockingFindings...)
			}
			if a.Process != nil {
				projected.ExitCode = a.Process.ExitCode
				projected.Signal = a.Process.Signal
			}
			found := false
			for j := range w.FeatureAttempts {
				if w.FeatureAttempts[j].ID == a.ID {
					w.FeatureAttempts[j] = projected
					found = true
					break
				}
			}
			if !found {
				w.FeatureAttempts = append(w.FeatureAttempts, projected)
			}
		}
	}
	w.State = "implementing"
	graphJSON, _ = json.Marshal(g)
	workJSON, _ = json.Marshal(w)
	if _, err = tx.ExecContext(ctx, `UPDATE factory_execution_graphs SET document=$3,version=version+1,updated_at=now() WHERE account_id=$1 AND work_id=$2`, account, workID, graphJSON); err != nil {
		return Snapshot{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_work_items SET document=$3,state=$4,updated_at=now() WHERE account_id=$1 AND id=$2`, account, workID, workJSON, w.State); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Version: current + 1, Graph: g}, tx.Commit()
}
