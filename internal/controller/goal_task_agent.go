package controller

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func (s *Store) boxGoalTaskActor(ctx context.Context, p Principal, creator string) (goalTaskActor, error) {
	a := goalTaskActor{Principal: p, Kind: "box", ID: creator}
	a.Role = "task-box"
	caps, err := s.EffectiveAgentCapabilities(ctx, p.AccountID, creator)
	if err != nil {
		return a, err
	}
	if !caps.CreateAgentBox.Enabled {
		return a, fmt.Errorf("task creation permission revoked")
	}
	a.CreateGrant = &caps.CreateAgentBox
	a.AllowControl = caps.ManageAgentBoxes.Control
	a.AllowInspect = caps.ManageAgentBoxes.Inspect
	if err = s.DB.QueryRowContext(ctx, `SELECT name FROM logical_boxes WHERE account_id=$1 AND id=$2`, p.AccountID, creator).Scan(&a.Name); err != nil {
		return a, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT public_id,application FROM login_profiles WHERE account_id=$1`, p.AccountID)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, app string
		if err = rows.Scan(&id, &app); err != nil {
			return a, err
		}
		if app == "github" || slices.Contains(caps.CreateAgentBox.AllowedAgents, app) {
			a.ProfileIDs = append(a.ProfileIDs, id)
		}
	}
	return a, rows.Err()
}
func validateBoxGoalTask(a goalTaskActor, req v1.CreateGoalTaskRequest) error {
	if a.CreateGrant == nil || !a.CreateGrant.Enabled || !slices.Contains(a.CreateGrant.AllowedAgents, req.Agent) || req.DiskGiB > int64(a.CreateGrant.MaxDiskGiB) {
		return fmt.Errorf("task exceeds box creation permissions")
	}
	for _, role := range req.RoleIDs {
		if !slices.Contains(a.CreateGrant.AssignableRoleIDs, role) {
			return fmt.Errorf("role is not assignable by this box")
		}
	}
	if req.MemoryGiB > 8 || (req.SwapGiB != nil && *req.SwapGiB > 4) {
		return fmt.Errorf("memory or swap exceeds box creation limits")
	}
	if req.Links != nil {
		for _, kind := range req.Links.Kinds {
			if (kind == "screenshot" && !a.AllowInspect) || (kind != "screenshot" && !a.AllowControl) {
				return fmt.Errorf("task link permission required")
			}
		}
	}
	return nil
}
func (s *Server) createBoxGoalTask(w http.ResponseWriter, r *http.Request, p Principal, req v1.CreateGoalTaskRequest, profiles []v1.LoginProfileRef, key string) {
	a, err := s.Store.boxGoalTaskActor(r.Context(), p, agentBoxID(p))
	if err != nil {
		writeError(w, 403, fmt.Errorf("task creation unavailable"))
		return
	}
	for _, profile := range profiles {
		var id string
		if err = s.Store.DB.QueryRowContext(r.Context(), `SELECT public_id FROM login_profiles WHERE account_id=$1 AND application=$2 AND name=$3`, p.AccountID, profile.Application, profile.Name).Scan(&id); err != nil {
			writeError(w, 400, fmt.Errorf("selected profile unavailable"))
			return
		}
		req.LoginProfiles = append(req.LoginProfiles, v1.TaskProfileRef{ProfileID: id, Model: profile.Model, ReasoningEffort: profile.ReasoningEffort})
	}
	if err = normalizeGoalTask(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	if err = validateBoxGoalTask(a, req); err != nil {
		writeError(w, 403, err)
		return
	}
	if _, err = s.Store.resolveGoalTaskProfiles(r.Context(), a, req); err != nil {
		writeError(w, 400, err)
		return
	}
	if err = s.validateGoalTaskAdmission(r.Context(), a, req); err != nil {
		writeError(w, 400, err)
		return
	}
	id, reused, err := s.Store.createGoalTask(r.Context(), a, req, key)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	t, err := s.Store.goalTask(r.Context(), a, id)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	if reused {
		w.Header().Set("Idempotency-Replayed", "true")
	} else if req.Links != nil {
		t.Links, err = s.mintGoalTaskLinks(r.Context(), t, *req.Links)
		if err != nil {
			goalTaskHTTPError(w, err)
			return
		}
	}
	writeJSON(w, 202, map[string]any{"task": t})
}
