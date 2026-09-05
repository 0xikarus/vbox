package controller

import (
	"context"
	"fmt"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// The primary is a box-wide preference by exact name, so it survives client
// changes and volume restoration. Live handoffs still validate incarnations.
func (s *Store) PrimarySession(ctx context.Context, p Principal, id string) (string, error) {
	box, err := s.LogicalBox(ctx, p, id)
	if err != nil {
		return "", err
	}
	var name string
	err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(metadata->>'primarySession','') FROM logical_boxes WHERE account_id=$1 AND id=$2`, p.AccountID, box.ID).Scan(&name)
	return name, err
}

func (s *Store) rememberPrimarySession(ctx context.Context, p Principal, a fleetAssignment, name string) error {
	r, err := s.DB.ExecContext(ctx, `UPDATE logical_boxes SET metadata=jsonb_set(metadata,'{primarySession}',to_jsonb($3::text)),updated_at=now() WHERE account_id=$1 AND id=$2 AND state='running' AND slot_id=$4 AND assignment_generation=$5 AND fencing_token=$6`, p.AccountID, a.Box.ID, name, a.Slot.ID, a.Box.AssignmentGeneration, a.FencingToken)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return fmt.Errorf("box assignment changed; primary session was not saved")
	}
	return nil
}

func selectedPrimary(inv v1.SessionInventory, id, incarnation string) (string, error) {
	if inv.State != "live" || id == "" || incarnation == "" {
		return "", fmt.Errorf("select a live session")
	}
	for _, session := range inv.Sessions {
		if session.ID == id && session.Incarnation == incarnation {
			return session.Name, nil
		}
	}
	return "", fmt.Errorf("selected session disappeared or was recreated; select again")
}

func (s *Server) primarySessionHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == http.MethodGet {
		name, err := s.Store.PrimarySession(r.Context(), p, r.PathValue("id"))
		if err != nil {
			writeError(w, 404, err)
			return
		}
		writeJSON(w, 200, map[string]string{"session": name})
		return
	}
	var req struct {
		SessionID   string `json:"sessionId"`
		Incarnation string `json:"incarnation"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	inv, err := s.observeSessions(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 502, err)
		return
	}
	name, err := selectedPrimary(inv, req.SessionID, req.Incarnation)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	a, err := s.Store.assignment(r.Context(), p.AccountID, inv.LogicalBoxID)
	if err != nil || nativeFence(a) != inv.Assignment {
		writeError(w, 409, fmt.Errorf("box assignment changed"))
		return
	}
	if err = s.Store.rememberPrimarySession(r.Context(), p, a, name); err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]string{"session": name})
}
