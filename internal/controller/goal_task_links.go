package controller

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type goalTaskLinkScope struct {
	ID, TaskID, Kind, BoxID string
	Principal
}
type goalTaskLinkContextKey struct{}

func (s *Server) registerGoalTaskLinkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/tasks/{task}/links", s.goalTaskAuth(s.goalTaskLinksHandler))
	mux.HandleFunc("DELETE /v1/tasks/{task}/links", s.goalTaskAuth(s.goalTaskLinksHandler))
	mux.HandleFunc("GET /t/{token}", s.exchangeGoalTaskLink)
	mux.HandleFunc("GET /t", uiHandler("task-link.html", "text/html; charset=utf-8", false))
	mux.HandleFunc("GET /v1/task-links/{token}/screenshot.png", s.goalTaskLinkAuth(false, s.goalTaskLinkScreenshot))
	mux.HandleFunc("GET /v1/task-links/state", s.goalTaskLinkAuth(true, s.goalTaskLinkState))
	mux.HandleFunc("GET /v1/task-links/snapshot", s.goalTaskLinkAuth(true, s.goalTaskLinkScreenshot))
	for _, kind := range []string{"desktop", "terminal"} {
		mux.HandleFunc("GET /v1/task-links/"+kind+"/stream", s.goalTaskLinkAuth(true, func(w http.ResponseWriter, r *http.Request, scope goalTaskLinkScope) {
			if scope.BoxID == "" {
				writeError(w, 409, fmt.Errorf("task has no live workspace"))
				return
			}
			var state string
			if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT state FROM goal_tasks WHERE id=$1`, scope.TaskID).Scan(&state); err != nil || state != "running" {
				writeError(w, 409, fmt.Errorf("task is not running"))
				return
			}
			r.SetPathValue("id", scope.BoxID)
			r = r.WithContext(context.WithValue(r.Context(), goalTaskLinkContextKey{}, scope))
			s.workspaceStream(w, r, scope.Principal, kind == "desktop")
		}))
	}
}

func (s *Server) mintGoalTaskLinks(ctx context.Context, t v1.GoalTask, req v1.TaskLinkRequest) ([]v1.GoalTaskLink, error) {
	if err := validateGoalTaskLinks(&req); err != nil {
		return nil, err
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Lock with finish so that newly minted links cannot outlive retention.
	var deleteAt *time.Time
	if err = tx.QueryRowContext(ctx, `SELECT delete_at FROM goal_tasks WHERE id=$1 FOR UPDATE`, t.ID).Scan(&deleteAt); err != nil {
		return nil, err
	}
	expiry := time.Now().UTC().Add(time.Duration(req.TTLSeconds) * time.Second)
	if deleteAt != nil && deleteAt.Before(expiry) {
		expiry = *deleteAt
	}
	links := []v1.GoalTaskLink{}
	for _, kind := range req.Kinds {
		token, hash, err := taskRandomToken("")
		if err != nil {
			return nil, err
		}
		link := v1.GoalTaskLink{ID: uuid(), Kind: kind, ExpiresAt: expiry}
		link.URL = strings.TrimRight(s.PublicURL, "/") + "/t/" + token
		if kind == "screenshot" {
			link.URL = strings.TrimRight(s.PublicURL, "/") + "/v1/task-links/" + token + "/screenshot.png"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO goal_task_links(id,task_id,token_hash,kind,expires_at) VALUES($1,$2,$3,$4,$5)`, link.ID, t.ID, hash, kind, expiry); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, tx.Commit()
}

func (s *Server) goalTaskLinksHandler(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	t, err := s.Store.goalTask(r.Context(), a, r.PathValue("task"))
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	if r.Method == http.MethodDelete {
		_, err = s.Store.DB.ExecContext(r.Context(), `UPDATE goal_task_links SET revoked_at=COALESCE(revoked_at,now()) WHERE task_id=$1 AND ($2='' OR id::text=$2)`, t.ID, r.URL.Query().Get("id"))
		if err != nil {
			goalTaskHTTPError(w, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	var req v1.TaskLinkRequest
	if err = decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if err = validateGoalTaskLinks(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	for _, kind := range req.Kinds {
		if kind == "control" && !a.AllowControl {
			writeError(w, 403, fmt.Errorf("control link capability required"))
			return
		}
	}
	links, err := s.mintGoalTaskLinks(r.Context(), t, req)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	writeJSON(w, 201, links)
}

const goalTaskLinkSelect = `SELECT l.id::text,l.task_id::text,l.kind,g.account_id::text,g.user_id::text,COALESCE(g.logical_box_id::text,'') FROM goal_task_links l JOIN goal_tasks g ON g.id=l.task_id `
const goalTaskLinkValid = `l.revoked_at IS NULL AND l.expires_at>now() AND (g.logical_box_id IS NOT NULL OR g.box_deleted_at IS NULL OR l.kind='screenshot')`

func scanGoalTaskLink(row *sql.Row) (goalTaskLinkScope, error) {
	var scope goalTaskLinkScope
	err := row.Scan(&scope.ID, &scope.TaskID, &scope.Kind, &scope.AccountID, &scope.UserID, &scope.BoxID)
	scope.Role = "owner" // Only used behind this capability's explicit route allowlist.
	return scope, err
}
func (s *Server) findGoalTaskLink(ctx context.Context, token string) (goalTaskLinkScope, error) {
	if len(token) != 64 {
		return goalTaskLinkScope{}, sql.ErrNoRows
	}
	hash := sha256.Sum256([]byte(token))
	return scanGoalTaskLink(s.Store.DB.QueryRowContext(ctx, goalTaskLinkSelect+`WHERE l.token_hash=$1 AND `+goalTaskLinkValid, hash[:]))
}
func taskLinkCookieName(id string) string { return "vbox_task_" + strings.ReplaceAll(id, "-", "") }
func (s *Server) exchangeGoalTaskLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	scope, err := s.findGoalTaskLink(r.Context(), r.PathValue("token"))
	if err != nil || scope.Kind == "screenshot" {
		writeError(w, 404, fmt.Errorf("link unavailable"))
		return
	}
	token, hash, err := taskRandomToken("")
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	var expiry time.Time
	err = s.Store.DB.QueryRowContext(r.Context(), `INSERT INTO goal_task_link_sessions(token_hash,link_id,expires_at) SELECT $1,id,expires_at FROM goal_task_links WHERE id=$2 RETURNING expires_at`, hash, scope.ID).Scan(&expiry)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: taskLinkCookieName(scope.ID), Value: token, Path: "/v1/task-links/", Expires: expiry, HttpOnly: true, Secure: strings.HasPrefix(s.PublicURL, "https://"), SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/t#"+scope.ID, http.StatusSeeOther)
}
func (s *Server) goalTaskLinkAuth(cookie bool, next func(http.ResponseWriter, *http.Request, goalTaskLinkScope)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		var scope goalTaskLinkScope
		var err error
		if cookie {
			id := r.URL.Query().Get("link")
			c, e := r.Cookie(taskLinkCookieName(id))
			if e != nil || len(c.Value) != 64 {
				writeError(w, 401, fmt.Errorf("link session unavailable"))
				return
			}
			hash := sha256.Sum256([]byte(c.Value))
			scope, err = scanGoalTaskLink(s.Store.DB.QueryRowContext(r.Context(), goalTaskLinkSelect+`JOIN goal_task_link_sessions s ON s.link_id=l.id WHERE s.token_hash=$1 AND l.id::text=$2 AND s.expires_at>now() AND l.kind IN ('control','view') AND `+goalTaskLinkValid, hash[:], id))
		} else {
			scope, err = s.findGoalTaskLink(r.Context(), r.PathValue("token"))
			if err == nil && scope.Kind != "screenshot" {
				err = sql.ErrNoRows
			}
		}
		if err != nil {
			writeError(w, 404, fmt.Errorf("link unavailable"))
			return
		}
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if _, err = s.Store.DB.ExecContext(r.Context(), `INSERT INTO audit_log(account_id,action,target_type,target_id,detail) VALUES($1,'task.link.use','task_link',$2,jsonb_build_object('kind',$3::text,'ip',$4::text))`, scope.AccountID, scope.ID, scope.Kind, ip); err != nil {
			goalTaskHTTPError(w, err)
			return
		}
		next(w, r, scope)
	}
}
func (s *Server) goalTaskLinkAlive(ctx context.Context, scope goalTaskLinkScope) bool {
	var alive bool
	err := s.Store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM goal_task_links l JOIN goal_tasks g ON g.id=l.task_id WHERE l.id=$1 AND g.logical_box_id::text=$2 AND g.state='running' AND `+goalTaskLinkValid+`)`, scope.ID, scope.BoxID).Scan(&alive)
	return err == nil && alive
}
func (s *Server) goalTaskLinkState(w http.ResponseWriter, r *http.Request, scope goalTaskLinkScope) {
	t, err := s.Store.goalTask(r.Context(), goalTaskActor{Principal: scope.Principal}, scope.TaskID)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	// Do not expose goals, results, profile selections, creators or other links.
	session := ""
	if t.Box != nil && t.State == "running" && t.Box.State == "running" {
		tasks, err := s.Store.ListBoxTasks(r.Context(), scope.Principal, t.Box.ID)
		if err != nil {
			goalTaskHTTPError(w, err)
			return
		}
		if len(tasks) > 0 && tasks[0].State == "active" {
			session = tasks[0].Session
		}
	}
	writeJSON(w, 200, map[string]any{"name": t.Name, "state": t.State, "box": t.Box, "kind": scope.Kind, "session": session, "queuePosition": t.QueuePosition})
}
func (s *Server) goalTaskLinkScreenshot(w http.ResponseWriter, r *http.Request, scope goalTaskLinkScope) {
	t, err := s.Store.goalTask(r.Context(), goalTaskActor{Principal: scope.Principal}, scope.TaskID)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	terminal := r.URL.Query().Get("source") == "terminal"
	if t.State != "running" || t.Box == nil || t.Box.State != "running" {
		r.SetPathValue("task", scope.TaskID)
		if terminal {
			s.goalTaskTerminalHandler(w, r, goalTaskActor{Principal: scope.Principal})
		} else {
			s.goalTaskScreenshotHandler(w, r, goalTaskActor{Principal: scope.Principal})
		}
		return
	}
	a, err := s.Store.assignment(r.Context(), scope.AccountID, scope.BoxID)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	prov, err := s.provider(r.Context(), scope.AccountID, a.Box.Provider, a.Box.ProviderCredential)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	var data []byte
	if terminal {
		tasks, e := s.Store.ListBoxTasks(ctx, scope.Principal, scope.BoxID)
		if e != nil || len(tasks) == 0 {
			writeError(w, 409, fmt.Errorf("terminal unavailable"))
			return
		}
		result, e := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "tmux-screen", tasks[0].Session, "500"}, provider.ExecOptions{})
		var snap v1.TerminalSnapshot
		if e != nil || result.ExitCode != 0 || json.Unmarshal([]byte(result.Stdout), &snap) != nil {
			writeError(w, 502, fmt.Errorf("terminal unavailable"))
			return
		}
		data = []byte(snap.Content)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		data, err = readDesktopCapture(ctx, prov, a.Slot.ServiceID, nativeFence(a), false)
		if err != nil {
			writeError(w, 502, fmt.Errorf("desktop unavailable"))
			return
		}
		w.Header().Set("Content-Type", "image/png")
	}
	current, err := s.Store.assignment(ctx, scope.AccountID, scope.BoxID)
	if err != nil || nativeFence(a) != nativeFence(current) || !s.goalTaskLinkAlive(ctx, scope) {
		writeError(w, 409, fmt.Errorf("task changed"))
		return
	}
	w.Header().Set("X-Vbox-Captured-At", time.Now().UTC().Format(time.RFC3339Nano))
	w.Write(data)
}
