package controller

import (
	"context"
	"fmt"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// Persist observations after taking a box lock. Late probes cannot overwrite
// newer observations; unknown/offline probes cannot imply session completion.
func (s *Store) RecordSessionObservation(ctx context.Context, p Principal, inv v1.SessionInventory) error {
	if inv.State != "live" {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation int64
	var fence string
	err = tx.QueryRowContext(ctx, `SELECT assignment_generation,COALESCE(fencing_token,'') FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state='running' FOR UPDATE`, p.AccountID, inv.LogicalBoxID).Scan(&generation, &fence)
	if err != nil {
		return err
	}
	a := fleetAssignment{Box: v1.LogicalBox{ID: inv.LogicalBoxID, AssignmentGeneration: generation}, FencingToken: fence}
	if nativeFence(a) != inv.Assignment {
		return fmt.Errorf("assignment changed before observation commit")
	}
	watermark, err := tx.ExecContext(ctx, `INSERT INTO session_probe_watermarks(account_id,box_id,observed_at) VALUES($1,$2,$3) ON CONFLICT(account_id,box_id) DO UPDATE SET observed_at=excluded.observed_at WHERE session_probe_watermarks.observed_at<excluded.observed_at`, p.AccountID, inv.LogicalBoxID, inv.ObservedAt)
	if err != nil {
		return err
	}
	advanced, _ := watermark.RowsAffected()
	if advanced == 0 {
		return nil
	}
	for _, session := range inv.Sessions {
		_, err = tx.ExecContext(ctx, `INSERT INTO session_observations(account_id,box_id,incarnation,session_name,fingerprint,state,revision,observed_at,partial) VALUES($1,$2,$3,$4,$5,'baseline',$6,$7,$8)
 ON CONFLICT(account_id,box_id,incarnation) DO UPDATE SET session_name=excluded.session_name,fingerprint=excluded.fingerprint,state='changed',revision=excluded.revision,sequence=nextval('session_observations_sequence_seq'),observed_at=excluded.observed_at,partial=excluded.partial
 WHERE session_observations.observed_at < excluded.observed_at AND (session_observations.fingerprint<>excluded.fingerprint OR session_observations.session_name<>excluded.session_name OR session_observations.partial<>excluded.partial OR session_observations.state='exited')`, p.AccountID, inv.LogicalBoxID, session.Incarnation, session.Name, session.Fingerprint, uuid(), inv.ObservedAt, session.Partial)
		if err != nil {
			return err
		}
	}
	if !inv.Partial {
		present := []string{}
		for _, session := range inv.Sessions {
			present = append(present, session.Incarnation)
		}
		_, err = tx.ExecContext(ctx, `UPDATE session_observations SET state='exited',revision=gen_random_uuid(),sequence=nextval('session_observations_sequence_seq'),observed_at=$3 WHERE account_id=$1 AND box_id=$2 AND state<>'exited' AND observed_at<$3 AND NOT (incarnation=ANY($4::text[]))`, p.AccountID, inv.LogicalBoxID, inv.ObservedAt, present)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SessionUpdates(ctx context.Context, p Principal, box string) ([]v1.SessionUpdate, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT o.box_id::text,o.session_name,o.incarnation,o.revision::text,o.state,o.observed_at,o.partial FROM session_observations o LEFT JOIN session_acknowledgements a ON a.account_id=o.account_id AND a.user_id=$2 AND a.box_id=o.box_id AND a.incarnation=o.incarnation WHERE o.account_id=$1 AND o.box_id=$3 AND o.sequence>COALESCE(a.sequence,0) ORDER BY o.sequence LIMIT 256`, p.AccountID, p.UserID, box)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []v1.SessionUpdate{}
	for rows.Next() {
		var v v1.SessionUpdate
		if err = rows.Scan(&v.LogicalBoxID, &v.Session, &v.Incarnation, &v.Revision, &v.State, &v.ObservedAt, &v.Partial); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

func (s *Server) updatesHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	inv, err := s.observeSessions(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 502, err)
		return
	}
	if err = s.Store.RecordSessionObservation(r.Context(), p, inv); err != nil {
		writeError(w, 409, err)
		return
	}
	updates, err := s.Store.SessionUpdates(r.Context(), p, inv.LogicalBoxID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, v1.SessionUpdates{Inventory: inv, Updates: updates, Limit: "On-demand snapshots: 64 sessions, 128 panes, 200 history lines per pane, 256 unread revisions. May miss transient output; not agent completion or needs-input."})
}

func (s *Server) ackUpdateHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var req struct {
		Session  string `json:"session"`
		Revision string `json:"revision"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	// Select only the exact extant revision. Concurrent newer output makes an
	// old acknowledgement fail, never marks newer output read.
	result, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO session_acknowledgements(account_id,user_id,box_id,incarnation,sequence) SELECT account_id,$2,box_id,incarnation,sequence FROM session_observations WHERE account_id=$1 AND box_id=$3 AND session_name=$4 AND revision::text=$5 ON CONFLICT(account_id,user_id,box_id,incarnation) DO UPDATE SET sequence=GREATEST(session_acknowledgements.sequence,excluded.sequence)`, p.AccountID, p.UserID, box.ID, req.Session, req.Revision)
	if err != nil {
		writeError(w, 500, fmt.Errorf("acknowledgement failed"))
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		writeError(w, 409, fmt.Errorf("revision is stale, foreign, or unknown; refresh updates"))
		return
	}
	writeJSON(w, 200, map[string]bool{"acknowledged": true})
}
