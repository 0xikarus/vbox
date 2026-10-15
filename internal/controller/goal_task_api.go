package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func (s *Server) registerGoalTaskRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /tasks", uiHandler("tasks.html", "text/html; charset=utf-8", false))
	mux.HandleFunc("GET /v1/task-service-credentials/profiles", s.owner(s.goalTaskProfileMapping))
	mux.HandleFunc("GET /v1/tasks/profiles", s.goalTaskAuth(s.goalTaskProfiles))
	mux.HandleFunc("POST /v1/tasks", s.goalTaskAuth(s.createGoalTaskHandler))
	mux.HandleFunc("GET /v1/tasks", s.goalTaskAuth(s.listGoalTasksHandler))
	mux.HandleFunc("GET /v1/tasks/{task}", s.goalTaskAuth(s.getGoalTaskHandler))
	for _, method := range []string{"GET", "POST"} {
		mux.HandleFunc(method+" /v1/tasks/{task}/messages", s.goalTaskAuth(s.goalTaskMessagesHandler))
	}
	mux.HandleFunc("GET /v1/tasks/{task}/events", s.goalTaskAuth(s.goalTaskEventsHandler))
	mux.HandleFunc("POST /v1/tasks/{task}/cancel", s.goalTaskAuth(s.cancelGoalTaskHandler))
	mux.HandleFunc("DELETE /v1/tasks/{task}", s.goalTaskAuth(s.deleteGoalTaskHandler))
	mux.HandleFunc("DELETE /v1/tasks/{task}/box", s.goalTaskAuth(s.deleteGoalTaskHandler))
	mux.HandleFunc("GET /v1/tasks/{task}/files/{file}", s.goalTaskAuth(s.goalTaskFileHandler))
	mux.HandleFunc("GET /v1/tasks/{task}/final-screenshot", s.goalTaskAuth(s.goalTaskScreenshotHandler))
	mux.HandleFunc("GET /v1/tasks/{task}/final-terminal", s.goalTaskAuth(s.goalTaskTerminalHandler))
	for _, method := range []string{"GET", "POST"} {
		mux.HandleFunc(method+" /v1/task-service-credentials", s.owner(s.goalTaskCredentials))
	}
	mux.HandleFunc("DELETE /v1/task-service-credentials/{credential}", s.owner(s.goalTaskCredentials))
	mux.HandleFunc("POST /v1/agent-desktop/task/complete", s.desktopAgentAuth(s.completeGoalTaskHandler))
	mux.HandleFunc("POST /v1/agent-desktop/task/fail", s.desktopAgentAuth(s.completeGoalTaskHandler))
	s.registerGoalTaskLinkRoutes(mux)
	s.registerGoalTaskProfileRoutes(mux)
}

func goalTaskHTTPError(w http.ResponseWriter, err error) {
	status := 500
	if errors.Is(err, errGoalTaskMissing) || errors.Is(err, sql.ErrNoRows) {
		status = 404
		err = errGoalTaskMissing
	} else if errors.Is(err, errGoalTaskQuota) {
		status = 403
	} else if errors.Is(err, errGoalTaskConflict) || errors.Is(err, errGoalTaskIdempotency) {
		status = 409
	} else {
		err = fmt.Errorf("task operation unavailable")
	}
	writeError(w, status, err)
}

func (s *Server) createGoalTaskHandler(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	key, err := requireIdempotency(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var req v1.CreateGoalTaskRequest
	if err = decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	var requested []v1.CreateGoalTaskRequest
	if req.TaskProfile != "" {
		original := req
		if req.Links != nil {
			links := *req.Links
			links.Kinds = append([]string(nil), req.Links.Kinds...)
			original.Links = &links
		}
		requested = append(requested, original)
		// Replays use the original request, even after the profile changes.
		if id, found, replayErr := s.Store.replayTaskProfileRequest(r.Context(), a, req, key); replayErr != nil {
			goalTaskHTTPError(w, replayErr)
			return
		} else if found {
			task, e := s.Store.goalTask(r.Context(), a, id)
			if e != nil {
				goalTaskHTTPError(w, e)
				return
			}
			w.Header().Set("Idempotency-Replayed", "true")
			writeJSON(w, 201, task)
			return
		}
		req, err = s.Store.expandTaskProfile(r.Context(), a, req)
		if err != nil {
			if errors.Is(err, errGoalTaskProfileOverrides) {
				writeError(w, 400, err)
			} else {
				taskProfileError(w, err)
			}
			return
		}
	}
	if err = normalizeGoalTask(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	if len(req.RoleIDs) > 0 && a.Role != "owner" {
		writeError(w, 403, fmt.Errorf("only owners may assign task roles"))
		return
	}
	if req.Links != nil {
		for _, kind := range req.Links.Kinds {
			if kind == "control" && !a.AllowControl {
				writeError(w, 403, fmt.Errorf("control link capability required"))
				return
			}
		}
	}
	if _, err = s.Store.resolveGoalTaskProfiles(r.Context(), a, req); err != nil {
		writeError(w, 400, err)
		return
	}
	if err = s.validateGoalTaskAdmission(r.Context(), a, req); err != nil {
		writeError(w, 400, err)
		return
	}
	id, reused, err := s.Store.createGoalTask(r.Context(), a, req, key, requested...)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	task, err := s.Store.goalTask(r.Context(), a, id)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	if reused {
		w.Header().Set("Idempotency-Replayed", "true")
	} else if req.Links != nil {
		task.Links, err = s.mintGoalTaskLinks(r.Context(), task, *req.Links)
		if err != nil {
			goalTaskHTTPError(w, err)
			return
		}
	}
	writeJSON(w, 201, task)
}

func (s *Server) getGoalTaskHandler(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	task, err := s.Store.goalTask(r.Context(), a, r.PathValue("task"))
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	writeJSON(w, 200, task)
}

func (s *Server) listGoalTasksHandler(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 100 {
			writeError(w, 400, fmt.Errorf("limit must be 1–100"))
			return
		}
		limit = n
	}
	state := r.URL.Query().Get("state")
	creator := r.URL.Query().Get("creator")
	var before *time.Time
	cursorID := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		stamp, id, ok := strings.Cut(cursor, "|")
		t, e := time.Parse(time.RFC3339Nano, stamp)
		if !ok || e != nil {
			writeError(w, 400, fmt.Errorf("invalid cursor"))
			return
		}
		before = &t
		cursorID = id
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT `+goalTaskColumns+` FROM goal_tasks g LEFT JOIN logical_boxes b ON b.id=g.logical_box_id WHERE g.account_id=$1 AND ($2 OR (g.creator_kind=$3 AND g.creator_id=$4)) AND ($5='' OR g.state=$5 OR ($5='current' AND g.state IN ('queued','starting','running')) OR ($5='history' AND g.state IN ('completed','failed','timed_out','abandoned','cancelled'))) AND ($6='' OR g.creator_id=$6) AND ($7::timestamptz IS NULL OR (g.created_at,g.id::text)<($7,$8)) ORDER BY g.created_at DESC,g.id DESC LIMIT $9`, a.AccountID, a.Role == "owner", a.Kind, a.ID, state, creator, before, cursorID, limit+1)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	defer rows.Close()
	tasks := []v1.GoalTask{}
	for rows.Next() {
		t, err := scanGoalTask(rows)
		if err != nil {
			goalTaskHTTPError(w, err)
			return
		}
		tasks = append(tasks, t)
	}
	if err = rows.Err(); err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	next := ""
	if len(tasks) > limit {
		tasks = tasks[:limit]
		next = goalTaskCursor(tasks[len(tasks)-1])
	}
	writeJSON(w, 200, map[string]any{"tasks": tasks, "nextCursor": next})
}

func (s *Server) cancelGoalTaskHandler(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	task, err := s.Store.goalTask(r.Context(), a, r.PathValue("task"))
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	if v1.GoalTaskFinished(task.State) && task.State != "cancelled" {
		goalTaskHTTPError(w, errGoalTaskConflict)
		return
	}
	if _, err = s.Store.finishGoalTask(r.Context(), a.AccountID, task.ID, "cancelled", "Cancelled by creator", "cancelled", nil); err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	task, err = s.Store.goalTask(r.Context(), a, task.ID)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	writeJSON(w, 200, task)
}

func (s *Server) deleteGoalTaskHandler(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	task, err := s.Store.goalTask(r.Context(), a, r.PathValue("task"))
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	if !v1.GoalTaskFinished(task.State) {
		writeError(w, 409, fmt.Errorf("cancel the active task before deleting it"))
		return
	}
	onlyBox := strings.HasSuffix(r.URL.Path, "/box")
	_, err = s.Store.DB.ExecContext(r.Context(), `UPDATE goal_tasks SET delete_box_requested=true,delete_at=CASE WHEN $2 THEN delete_at ELSE now() END WHERE id=$1`, task.ID, onlyBox)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"id": task.ID, "deleting": true})
}

func (s *Server) goalTaskEventsHandler(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	t, err := s.Store.goalTask(r.Context(), a, r.PathValue("task"))
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	after := int64(0)
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			writeError(w, 400, fmt.Errorf("invalid event cursor"))
			return
		}
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT sequence,kind,text,created_at FROM goal_task_events WHERE task_id=$1 AND sequence>$2 ORDER BY sequence LIMIT 100`, t.ID, after)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	defer rows.Close()
	events := []map[string]any{}
	for rows.Next() {
		var seq int64
		var kind, text string
		var at time.Time
		if err = rows.Scan(&seq, &kind, &text, &at); err != nil {
			goalTaskHTTPError(w, err)
			return
		}
		events = append(events, map[string]any{"id": seq, "kind": kind, "text": text, "createdAt": at})
		after = seq
	}
	if err = rows.Err(); err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"events": events, "after": after})
}

func (s *Store) goalTaskTranscript(ctx context.Context, id string) (json.RawMessage, error) {
	var result json.RawMessage
	err := s.DB.QueryRowContext(ctx, `SELECT CASE WHEN g.logical_box_id IS NULL OR g.captured_at IS NOT NULL THEN g.transcript ELSE COALESCE((SELECT jsonb_agg(jsonb_build_object('id',m.id,'direction',m.direction,'text',m.body,'createdAt',m.created_at,'state',m.state) ORDER BY m.created_at,m.id) FROM box_messages m JOIN box_tasks t ON t.id=m.task_id WHERE t.logical_box_id=g.logical_box_id AND t.account_id=g.account_id),'[]'::jsonb) END FROM goal_tasks g WHERE g.id=$1`, id).Scan(&result)
	return result, err
}

func (s *Server) goalTaskMessagesHandler(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	t, err := s.Store.goalTask(r.Context(), a, r.PathValue("task"))
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		messages, err := s.Store.goalTaskTranscript(r.Context(), t.ID)
		if err != nil {
			goalTaskHTTPError(w, err)
			return
		}
		writeJSON(w, 200, messages)
		return
	}
	key, err := requireIdempotency(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var request struct {
		Text string `json:"text"`
	}
	if err = decodeJSON(r, &request); err != nil || strings.TrimSpace(request.Text) == "" || len(request.Text) > 20000 {
		writeError(w, 400, fmt.Errorf("text must contain 1–20000 bytes"))
		return
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	defer tx.Rollback()
	var state string
	if err = tx.QueryRowContext(r.Context(), `SELECT state FROM goal_tasks WHERE id=$1 FOR UPDATE`, t.ID).Scan(&state); err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	if state != "running" || t.Box == nil {
		writeError(w, 409, fmt.Errorf("task is not running"))
		return
	}
	tasks, err := s.Store.ListBoxTasks(r.Context(), goalTaskOwner(a), t.Box.ID)
	if err != nil || len(tasks) == 0 {
		writeError(w, 409, fmt.Errorf("agent is not ready"))
		return
	}
	task := tasks[0]
	message, reused, err := s.Store.CreateBoxMessage(r.Context(), goalTaskOwner(a), task.ID, "goal-message:"+t.ID+":"+key, v1.SendBoxMessageRequest{Text: request.Text})
	if err != nil {
		writeError(w, 409, fmt.Errorf("message could not be queued"))
		return
	}
	if reused && message.Text != request.Text {
		writeError(w, 409, errGoalTaskIdempotency)
		return
	}
	if !reused {
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO goal_task_events(task_id,kind,text) VALUES($1,'message',$2)`, t.ID, request.Text); err != nil {
			goalTaskHTTPError(w, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	if !reused {
		s.startBoxMessage(goalTaskOwner(a), task, message, true)
	}
	writeJSON(w, 202, message)
}

func (s *Server) goalTaskFileHandler(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	t, err := s.Store.goalTask(r.Context(), a, r.PathValue("task"))
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	var name, media string
	var data []byte
	err = s.Store.DB.QueryRowContext(r.Context(), `SELECT name,media_type,data FROM goal_task_files WHERE task_id=$1 AND id::text=$2`, t.ID, r.PathValue("file")).Scan(&name, &media, &data)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Write(data)
}

func (s *Server) goalTaskScreenshotHandler(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	t, err := s.Store.goalTask(r.Context(), a, r.PathValue("task"))
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	var data []byte
	var at *time.Time
	err = s.Store.DB.QueryRowContext(r.Context(), `SELECT final_screenshot,screenshot_at FROM goal_tasks WHERE id=$1`, t.ID).Scan(&data, &at)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	if len(data) == 0 {
		writeError(w, 409, fmt.Errorf("final screenshot unavailable"))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	if at != nil {
		w.Header().Set("X-Vbox-Captured-At", at.UTC().Format(time.RFC3339Nano))
	}
	w.Write(data)
}

func (s *Server) goalTaskTerminalHandler(w http.ResponseWriter, r *http.Request, a goalTaskActor) {
	t, err := s.Store.goalTask(r.Context(), a, r.PathValue("task"))
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	var text string
	if err = s.Store.DB.QueryRowContext(r.Context(), `SELECT final_terminal FROM goal_tasks WHERE id=$1`, t.ID).Scan(&text); err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	if text == "" {
		writeError(w, 409, fmt.Errorf("final terminal capture unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, text)
}

func (s *Server) completeGoalTaskHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var id, state string
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT id::text,state FROM goal_tasks WHERE account_id=$1 AND logical_box_id=$2`, p.AccountID, agentBoxID(p)).Scan(&id, &state)
	if err != nil {
		writeError(w, 404, fmt.Errorf("this box has no goal task"))
		return
	}
	var req struct {
		Text    string           `json:"text"`
		Message string           `json:"message"`
		Files   []goalTaskUpload `json:"files"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 280<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&req); err != nil {
		writeError(w, 400, fmt.Errorf("invalid task result"))
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, fmt.Errorf("unexpected content after task result"))
		return
	}
	outcome, text, reason := "completed", strings.TrimSpace(req.Text), ""
	if strings.HasSuffix(r.URL.Path, "/fail") {
		outcome, text, reason = "failed", strings.TrimSpace(req.Message), "agent"
		if len(req.Files) > 0 {
			writeError(w, 400, fmt.Errorf("failed task cannot include files"))
			return
		}
	}
	if text == "" || len(text) > 20000 {
		writeError(w, 400, fmt.Errorf("result text must contain 1–20000 bytes"))
		return
	}
	if state != "running" && !v1.GoalTaskFinished(state) {
		writeError(w, 409, fmt.Errorf("task has not started"))
		return
	}
	if _, err = s.Store.finishGoalTask(r.Context(), p.AccountID, id, outcome, text, reason, req.Files); err != nil {
		writeError(w, 400, fmt.Errorf("task result could not be recorded"))
		return
	}
	a := goalTaskActor{Principal: goalTaskOwner(goalTaskActor{Principal: p})}
	task, err := s.Store.goalTask(r.Context(), a, id)
	if err != nil {
		goalTaskHTTPError(w, err)
		return
	}
	writeJSON(w, 200, task)
}
