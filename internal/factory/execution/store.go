package execution

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/0xikarus/vmbox-service/internal/factory"
)

var ErrConflict = errors.New("feature execution revision or identity changed")
var ErrCapacity = errors.New("feature worker capacity is occupied")

type Store struct{ DB *sql.DB }
type Snapshot struct {
	Version int
	Graph   Graph
}

func (s Store) Migrate(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS factory_execution_graphs (
 account_id TEXT NOT NULL, work_id TEXT NOT NULL REFERENCES factory_work_items(id),
 version BIGINT NOT NULL, document JSONB NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(account_id,work_id))`)
	return err
}
func (s Store) Initialize(ctx context.Context, account, workID string) (Snapshot, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback()
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
func (s Store) Bind(ctx context.Context, account, workID string, version int, id, attemptID, boxID, taskID string) (Snapshot, error) {
	return s.mutate(ctx, account, workID, version, func(_ *sql.Tx, _ *factory.Work, g *Graph) error {
		n, err := g.node(id)
		if err != nil {
			return err
		}
		if n.Attempt == nil || n.Attempt.ID != attemptID || boxID == "" || taskID == "" {
			return ErrConflict
		}
		if n.Attempt.TaskID != "" && (n.Attempt.TaskID != taskID || n.Attempt.BoxID != boxID) {
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
func (s Store) accept(ctx context.Context, a, w string, v int, id, stage string, p Process, apply func(*Graph) error) (Snapshot, error) {
	return s.mutate(ctx, a, w, v, func(_ *sql.Tx, _ *factory.Work, g *Graph) error {
		n, err := g.node(id)
		if err != nil {
			return err
		}
		if n.Attempt == nil || n.Attempt.Stage != stage || n.Attempt.ID != p.AttemptID || n.Attempt.TaskID == "" || n.Attempt.TaskID != p.TaskID || n.Attempt.BoxID != p.BoxID {
			return ErrConflict
		}
		if err = apply(g); err != nil {
			return err
		}
		n.Attempt.State = "exited"
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
