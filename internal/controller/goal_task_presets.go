package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Server) registerGoalTaskProfileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/task-profiles", s.goalTaskAuth(s.listGoalTaskProfiles))
	mux.HandleFunc("GET /v1/task-profiles/{profile}", s.goalTaskAuth(s.getGoalTaskProfile))
	mux.HandleFunc("POST /v1/task-profiles", s.owner(s.saveGoalTaskProfile))
	mux.HandleFunc("PUT /v1/task-profiles/{profile}", s.owner(s.saveGoalTaskProfile))
	mux.HandleFunc("DELETE /v1/task-profiles/{profile}", s.owner(s.deleteGoalTaskProfile))
}

const goalTaskProfileColumns = `id::text,name,settings,created_at,updated_at`

func scanGoalTaskProfile(row interface{ Scan(...any) error }) (v1.GoalTaskProfile, error) {
	var p v1.GoalTaskProfile
	var raw []byte
	if err := row.Scan(&p.ID, &p.Name, &raw, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return p, err
	}
	return p, json.Unmarshal(raw, &p.Settings)
}

func (s *Store) taskProfile(ctx context.Context, a goalTaskActor, ref string) (v1.GoalTaskProfile, error) {
	p, err := scanGoalTaskProfile(s.DB.QueryRowContext(ctx, `SELECT `+goalTaskProfileColumns+` FROM goal_task_profiles WHERE account_id=$1 AND (id::text=$2 OR name=$2) ORDER BY (id::text=$2) DESC LIMIT 1`, a.AccountID, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return p, errGoalTaskProfileMissing
	}
	if err == nil && !s.mayUseTaskProfile(ctx, a, p) {
		return p, errGoalTaskProfileMissing
	}
	return p, err
}

var errGoalTaskProfileMissing = errors.New("task profile not found or not permitted")
var errGoalTaskProfileOverrides = errors.New("with taskProfile, supply only name, goal and optional links; edit the profile to change settings")

func (s *Store) mayUseTaskProfile(ctx context.Context, a goalTaskActor, p v1.GoalTaskProfile) bool {
	if a.Role == "owner" {
		return true
	}
	if len(p.Settings.RoleIDs) != 0 {
		return false
	}
	_, err := s.resolveGoalTaskProfiles(ctx, a, p.Settings)
	return err == nil
}

func taskProfileError(w http.ResponseWriter, err error) {
	if errors.Is(err, errGoalTaskProfileMissing) || errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, errGoalTaskProfileMissing)
	} else {
		writeError(w, 500, fmt.Errorf("task profiles unavailable"))
	}
}

func (s *Server) listGoalTaskProfiles(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT `+goalTaskProfileColumns+` FROM goal_task_profiles WHERE account_id=$1 ORDER BY name,id`, a.AccountID)
	if err != nil {
		taskProfileError(w, err)
		return
	}
	profiles := []v1.GoalTaskProfile{}
	for rows.Next() {
		p, e := scanGoalTaskProfile(rows)
		if e != nil {
			rows.Close()
			taskProfileError(w, e)
			return
		}
		profiles = append(profiles, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		taskProfileError(w, err)
		return
	}
	visible := []v1.GoalTaskProfile{}
	for _, p := range profiles {
		if s.Store.mayUseTaskProfile(r.Context(), a, p) {
			visible = append(visible, p)
		}
	}
	writeJSON(w, 200, visible)
}

func (s *Server) getGoalTaskProfile(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	p, err := s.Store.taskProfile(r.Context(), a, r.PathValue("profile"))
	if err != nil {
		taskProfileError(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) saveGoalTaskProfile(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Name     string                   `json:"name"`
		Settings v1.CreateGoalTaskRequest `json:"settings"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if provider.ValidateName(req.Name) != nil {
		writeError(w, 400, fmt.Errorf("profile name must be 1–63 letters, digits, dots, underscores or hyphens"))
		return
	}
	settings := req.Settings
	if settings.Name != "" || settings.Goal != "" || settings.TaskProfile != "" || settings.Links != nil {
		writeError(w, 400, fmt.Errorf("profile settings cannot include name, goal, taskProfile or links"))
		return
	}
	settings.Name, settings.Goal = "profile-validation", "Validate reusable settings"
	if err := normalizeGoalTask(&settings); err != nil {
		writeError(w, 400, err)
		return
	}
	a := goalTaskActor{Principal: p, Kind: "owner", ID: p.UserID}
	if err := s.validateGoalTaskConfiguration(r.Context(), a, settings); err != nil {
		writeError(w, 400, err)
		return
	}
	settings.Name, settings.Goal = "", ""
	raw, err := json.Marshal(settings)
	if err != nil {
		taskProfileError(w, err)
		return
	}
	var saved v1.GoalTaskProfile
	code := 201
	if r.Method == http.MethodPost {
		saved, err = scanGoalTaskProfile(s.Store.DB.QueryRowContext(r.Context(), `INSERT INTO goal_task_profiles(id,account_id,name,settings) VALUES($1,$2,$3,$4) ON CONFLICT(account_id,name) DO NOTHING RETURNING `+goalTaskProfileColumns, uuid(), p.AccountID, req.Name, raw))
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 409, fmt.Errorf("a task profile with that name already exists"))
			return
		}
	} else {
		previous, e := s.Store.taskProfile(r.Context(), a, r.PathValue("profile"))
		if e != nil {
			taskProfileError(w, e)
			return
		}
		// Names are stable API references. Create a new profile to use a new name.
		if previous.Name != req.Name {
			writeError(w, 400, fmt.Errorf("profile names cannot be changed; create a new profile"))
			return
		}
		code = 200
		saved, err = scanGoalTaskProfile(s.Store.DB.QueryRowContext(r.Context(), `UPDATE goal_task_profiles SET settings=$3,updated_at=now() WHERE account_id=$1 AND id=$2 RETURNING `+goalTaskProfileColumns, p.AccountID, previous.ID, raw))
	}
	if err != nil {
		taskProfileError(w, err)
		return
	}
	writeJSON(w, code, saved)
}

func (s *Server) deleteGoalTaskProfile(w http.ResponseWriter, r *http.Request, p Principal) {
	a := goalTaskActor{Principal: p, Kind: "owner", ID: p.UserID}
	profile, err := s.Store.taskProfile(r.Context(), a, r.PathValue("profile"))
	if err != nil {
		taskProfileError(w, err)
		return
	}
	if _, err = s.Store.DB.ExecContext(r.Context(), `DELETE FROM goal_task_profiles WHERE account_id=$1 AND id=$2`, p.AccountID, profile.ID); err != nil {
		taskProfileError(w, err)
		return
	}
	w.WriteHeader(204)
}

// Expansion snapshots settings into the task. Later edits/deletion never change
// an already queued task. Existing profile/role permissions are still enforced.
func (s *Store) expandTaskProfile(ctx context.Context, a goalTaskActor, req v1.CreateGoalTaskRequest) (v1.CreateGoalTaskRequest, error) {
	if req.TaskProfile == "" {
		return req, nil
	}
	rest := req
	rest.Name, rest.Goal, rest.TaskProfile, rest.Links = "", "", "", nil
	raw, _ := json.Marshal(rest)
	empty, _ := json.Marshal(v1.CreateGoalTaskRequest{})
	if string(raw) != string(empty) {
		return req, errGoalTaskProfileOverrides
	}
	p, err := s.taskProfile(ctx, a, req.TaskProfile)
	if err != nil {
		return req, err
	}
	settings := p.Settings
	settings.Name, settings.Goal, settings.TaskProfile, settings.Links = req.Name, req.Goal, p.Name, req.Links
	return settings, nil
}
