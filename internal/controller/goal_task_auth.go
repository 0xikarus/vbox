package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"net/http"
	"slices"
	"strings"
	"time"
)

type goalTaskActor struct {
	Principal
	Kind, ID, Name string
	ProfileIDs     []string
	AllowControl   bool
	AllowInspect   bool
	CreateGrant    *v1.CreateAgentBoxGrant
}

type goalTaskHandler func(http.ResponseWriter, *http.Request, goalTaskActor)

func taskRandomToken(prefix string) (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := prefix + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

func (s *Server) goalTaskAuth(next goalTaskHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := authorizationValue(r.Header.Get("Authorization"), "Bearer")
		if ok && strings.HasPrefix(token, "vbtask_") {
			hash := sha256.Sum256([]byte(token))
			var a goalTaskActor
			var profiles []byte
			err := s.Store.DB.QueryRowContext(r.Context(), `SELECT c.account_id::text,c.user_id::text,c.id::text,c.name,c.profile_ids,c.allow_control FROM task_service_credentials c JOIN users u ON u.id=c.user_id AND u.account_id=c.account_id WHERE c.token_hash=$1 AND c.revoked_at IS NULL AND (c.expires_at IS NULL OR c.expires_at>now()) AND u.disabled_at IS NULL`, hash[:]).Scan(&a.AccountID, &a.UserID, &a.ID, &a.Name, &profiles, &a.AllowControl)
			if err != nil || json.Unmarshal(profiles, &a.ProfileIDs) != nil {
				writeError(w, 401, fmt.Errorf("invalid task service credential"))
				return
			}
			a.Kind = "service"
			a.Role = "task-service"
			next(w, r, a)
			return
		}
		s.auth(func(w http.ResponseWriter, r *http.Request, p Principal) {
			next(w, r, goalTaskActor{Principal: p, Kind: p.Role, ID: p.UserID, Name: p.Subject, AllowControl: p.Role == "owner"})
		})(w, r)
	}
}

func (a goalTaskActor) maySelectProfile(id string) bool {
	return a.Role == "owner" || ((a.Kind == "service" || a.Kind == "box") && slices.Contains(a.ProfileIDs, id))
}

func (s *Server) goalTaskProfiles(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	type profile struct {
		ID          string `json:"id"`
		Application string `json:"application"`
		Label       string `json:"label"`
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT public_id,application FROM login_profiles WHERE account_id=$1 ORDER BY application,public_id`, a.AccountID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("profile catalog unavailable"))
		return
	}
	defer rows.Close()
	result := []profile{}
	for rows.Next() {
		var p profile
		if err := rows.Scan(&p.ID, &p.Application); err != nil {
			writeError(w, 500, fmt.Errorf("profile catalog unavailable"))
			return
		}
		if !a.maySelectProfile(p.ID) {
			continue
		}
		suffix := strings.TrimPrefix(p.ID, "lp_")
		p.Label = p.Application + " · " + strings.ToUpper(suffix[:min(8, len(suffix))])
		result = append(result, p)
	}
	if rows.Err() != nil {
		writeError(w, 500, fmt.Errorf("profile catalog unavailable"))
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) goalTaskCredentials(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT id::text,name,profile_ids,allow_control,expires_at,revoked_at,created_at FROM task_service_credentials WHERE account_id=$1 ORDER BY created_at DESC`, p.AccountID)
		if err != nil {
			writeError(w, 500, fmt.Errorf("credentials unavailable"))
			return
		}
		defer rows.Close()
		result := []map[string]any{}
		for rows.Next() {
			var id, name string
			var profiles json.RawMessage
			var control bool
			var expires, revoked *time.Time
			var created time.Time
			if err := rows.Scan(&id, &name, &profiles, &control, &expires, &revoked, &created); err != nil {
				writeError(w, 500, fmt.Errorf("credentials unavailable"))
				return
			}
			result = append(result, map[string]any{"id": id, "name": name, "profileIds": profiles, "allowControl": control, "expiresAt": expires, "revokedAt": revoked, "createdAt": created})
		}
		if rows.Err() != nil {
			writeError(w, 500, fmt.Errorf("credentials unavailable"))
			return
		}
		writeJSON(w, 200, result)
	case http.MethodPost:
		var req struct {
			Name         string     `json:"name"`
			ProfileIDs   []string   `json:"profileIds"`
			AllowControl bool       `json:"allowControl"`
			ExpiresAt    *time.Time `json:"expiresAt"`
		}
		if decodeJSON(r, &req) != nil || strings.TrimSpace(req.Name) == "" || len(req.Name) > 100 || len(req.ProfileIDs) > 100 || (req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now())) {
			writeError(w, 400, fmt.Errorf("valid name, profileIds and optional future expiry required"))
			return
		}
		for _, id := range req.ProfileIDs {
			var exists bool
			err := s.Store.DB.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM login_profiles WHERE account_id=$1 AND public_id=$2)`, p.AccountID, id).Scan(&exists)
			if err != nil || !exists {
				writeError(w, 400, fmt.Errorf("selected profile is unavailable"))
				return
			}
		}
		token, hash, err := taskRandomToken("vbtask_")
		if err != nil {
			writeError(w, 500, fmt.Errorf("credential generation failed"))
			return
		}
		id := uuid()
		ids, _ := json.Marshal(req.ProfileIDs)
		if _, err = s.Store.DB.ExecContext(r.Context(), `INSERT INTO task_service_credentials(id,account_id,user_id,name,token_hash,profile_ids,allow_control,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, p.AccountID, p.UserID, req.Name, hash, ids, req.AllowControl, req.ExpiresAt); err != nil {
			writeError(w, 500, fmt.Errorf("credential could not be saved"))
			return
		}
		writeJSON(w, 201, map[string]any{"id": id, "token": token, "name": req.Name, "profileIds": req.ProfileIDs, "allowControl": req.AllowControl, "expiresAt": req.ExpiresAt})
	case http.MethodDelete:
		res, err := s.Store.DB.ExecContext(r.Context(), `UPDATE task_service_credentials SET revoked_at=COALESCE(revoked_at,now()) WHERE account_id=$1 AND id::text=$2`, p.AccountID, r.PathValue("credential"))
		if err != nil {
			writeError(w, 500, fmt.Errorf("revocation failed"))
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			writeError(w, 404, fmt.Errorf("credential not found"))
			return
		}
		w.WriteHeader(204)
	}
}

func (s *Store) goalTaskActor(ctx context.Context, id string) (goalTaskActor, error) {
	var a goalTaskActor
	err := s.DB.QueryRowContext(ctx, `SELECT g.account_id::text,g.user_id::text,g.creator_kind,g.creator_id,g.creator_name FROM goal_tasks g JOIN users u ON u.id=g.user_id AND u.account_id=g.account_id WHERE g.id=$1 AND u.disabled_at IS NULL`, id).Scan(&a.AccountID, &a.UserID, &a.Kind, &a.ID, &a.Name)
	a.Role = "user"
	if a.Kind == "owner" {
		a.Role = "owner"
		a.AllowControl = true
	}
	if err == nil && a.Kind == "service" {
		var profiles []byte
		err = s.DB.QueryRowContext(ctx, `SELECT profile_ids,allow_control FROM task_service_credentials WHERE account_id=$1 AND id::text=$2 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now())`, a.AccountID, a.ID).Scan(&profiles, &a.AllowControl)
		if err == nil {
			err = json.Unmarshal(profiles, &a.ProfileIDs)
		}
	}
	if err == nil && a.Kind == "box" {
		return s.boxGoalTaskActor(ctx, a.Principal, a.ID)
	}
	return a, err
}

// Owner-only mapping; the task-service catalog deliberately never calls this.
func (s *Server) goalTaskProfileMapping(w http.ResponseWriter, r *http.Request, p Principal) {
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT public_id,application,name FROM login_profiles WHERE account_id=$1 ORDER BY application,name`, p.AccountID)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	defer rows.Close()
	result := []map[string]string{}
	for rows.Next() {
		var id, app, name string
		if err = rows.Scan(&id, &app, &name); err != nil {
			goalTaskHTTPError(w, err)
			return
		}
		suffix := strings.TrimPrefix(id, "lp_")
		result = append(result, map[string]string{"id": id, "application": app, "label": app + " · " + strings.ToUpper(suffix[:min(8, len(suffix))]), "name": name})
	}
	if err = rows.Err(); err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
