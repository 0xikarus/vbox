package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

var errGoalTaskMissing = errors.New("task not found")
var errGoalTaskConflict = errors.New("task is already finished")
var errGoalTaskIdempotency = errors.New("idempotency key already used with different task parameters")
var errGoalTaskQuota = errors.New("created-box limit reached")

func normalizeGoalTask(req *v1.CreateGoalTaskRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	req.Goal = strings.TrimSpace(req.Goal)
	if provider.ValidateName(req.Name) != nil || len(req.Name) > 100 {
		return fmt.Errorf("a valid task name is required")
	}
	if len(req.Goal) == 0 || len(req.Goal) > 100000 {
		return fmt.Errorf("goal must contain 1–100000 bytes")
	}
	if !slices.Contains([]string{"codex", "claude", "opencode"}, req.Agent) {
		return fmt.Errorf("agent must be codex, claude or opencode")
	}
	if req.TimeLimitSeconds == 0 {
		req.TimeLimitSeconds = 14400
	}
	if req.RetentionSeconds == 0 {
		req.RetentionSeconds = 86400
	}
	if req.DiskGiB == 0 {
		req.DiskGiB = 10
	}
	if req.TimeLimitSeconds < 300 || req.TimeLimitSeconds > 604800 || req.RetentionSeconds < 3600 || req.RetentionSeconds > 604800 {
		return fmt.Errorf("time limit must be 300–604800 seconds and retention 3600–604800 seconds")
	}
	if req.DiskGiB < 1 || req.DiskGiB > 1000 {
		return fmt.Errorf("diskGiB must be 1–1000")
	}
	if req.MemoryGiB != 0 || req.SwapGiB != nil {
		if req.MemoryGiB == 0 {
			req.MemoryGiB = 2
		}
		if req.SwapGiB == nil {
			v := int64(1)
			req.SwapGiB = &v
		}
		if req.MemoryGiB < 1 || req.MemoryGiB > v1.MaxBoxMemoryGiB || *req.SwapGiB < 0 || *req.SwapGiB > v1.MaxBoxMemoryGiB {
			return fmt.Errorf("invalid memory or swap limit")
		}
	}
	if req.DesktopResolution == "" {
		req.DesktopResolution = v1.DefaultDesktopResolution
	}
	if err := v1.ValidateDesktopResolution(req.DesktopResolution); err != nil {
		return err
	}
	if err := v1.ValidateInstructionMarkdown(goalTaskPreamble + "\n\n" + req.Instructions); err != nil {
		return err
	}
	if !slices.Contains(req.Tools, "desktop") {
		req.Tools = append(req.Tools, "desktop")
	}
	if err := v1.ValidateTools(req.Tools); err != nil {
		return err
	}
	if len(req.LoginProfiles) > 2 {
		return fmt.Errorf("select at most one agent and one GitHub profile")
	}
	if len(req.RoleIDs) > 8 {
		return fmt.Errorf("select at most eight roles")
	}
	if req.Links != nil {
		if err := validateGoalTaskLinks(req.Links); err != nil {
			return err
		}
	}
	slices.Sort(req.Tools)
	slices.Sort(req.RoleIDs)
	return nil
}

func validateGoalTaskLinks(req *v1.TaskLinkRequest) error {
	if req.TTLSeconds == 0 {
		req.TTLSeconds = 86400
	}
	if req.TTLSeconds < 1 || req.TTLSeconds > 604800 || len(req.Kinds) < 1 || len(req.Kinds) > 3 {
		return fmt.Errorf("choose 1–3 link kinds and a lifetime up to seven days")
	}
	seen := map[string]bool{}
	for _, kind := range req.Kinds {
		if !slices.Contains([]string{"control", "view", "screenshot"}, kind) || seen[kind] {
			return fmt.Errorf("invalid or duplicate link kind")
		}
		seen[kind] = true
	}
	slices.Sort(req.Kinds)
	return nil
}

func (s *Store) resolveGoalTaskProfiles(ctx context.Context, a goalTaskActor, req v1.CreateGoalTaskRequest) ([]v1.LoginProfileRef, error) {
	result := []v1.LoginProfileRef{}
	for _, ref := range req.LoginProfiles {
		if !a.maySelectProfile(ref.ProfileID) {
			return nil, fmt.Errorf("selected profile is unavailable")
		}
		var p v1.LoginProfileRef
		if err := s.DB.QueryRowContext(ctx, `SELECT application,name FROM login_profiles WHERE account_id=$1 AND public_id=$2`, a.AccountID, ref.ProfileID).Scan(&p.Application, &p.Name); err != nil {
			return nil, fmt.Errorf("selected profile is unavailable")
		}
		p.Model, p.ReasoningEffort = ref.Model, ref.ReasoningEffort
		result = append(result, p)
	}
	if err := validateAgentBoxProfiles(req.Agent, result); err != nil {
		return nil, fmt.Errorf("invalid profile selection for this agent")
	}
	return result, nil
}

func (s *Store) createGoalTask(ctx context.Context, a goalTaskActor, req v1.CreateGoalTaskRequest, key string, original ...v1.CreateGoalTaskRequest) (string, bool, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return "", false, err
	}
	requestRaw := raw
	if len(original) > 0 {
		requestRaw, err = json.Marshal(original[0])
		if err != nil {
			return "", false, err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	var account string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM accounts WHERE id=$1 FOR UPDATE`, a.AccountID).Scan(&account); err != nil {
		return "", false, err
	}
	var id string
	var same bool
	err = tx.QueryRowContext(ctx, `SELECT id::text,COALESCE(requested_settings,settings)=$5::jsonb FROM goal_tasks WHERE account_id=$1 AND creator_kind=$2 AND creator_id=$3 AND idempotency_key=$4`, a.AccountID, a.Kind, a.ID, key, requestRaw).Scan(&id, &same)
	if err == nil {
		if !same {
			return "", false, errGoalTaskIdempotency
		}
		return id, true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	if a.Kind == "box" {
		if err = validateBoxGoalTask(a, req); err != nil {
			return "", false, err
		}
		var creator string
		if err = tx.QueryRowContext(ctx, `SELECT id::text FROM logical_boxes WHERE account_id=$1 AND id::text=$2 AND state='running' FOR UPDATE`, a.AccountID, a.ID).Scan(&creator); err != nil {
			return "", false, err
		}
		var count int
		err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM agent_box_creations WHERE account_id=$1 AND creator_box_id::text=$2 AND (created_box_id IS NOT NULL OR completed_at IS NULL)) + (SELECT count(*) FROM goal_tasks WHERE account_id=$1 AND creator_kind='box' AND creator_id=$2 AND (logical_box_id IS NOT NULL OR state IN ('queued','starting','running')))`, a.AccountID, a.ID).Scan(&count)
		if err != nil {
			return "", false, err
		}
		if count >= a.CreateGrant.MaxBoxes {
			return "", false, errGoalTaskQuota
		}
	}
	id = uuid()
	_, err = tx.ExecContext(ctx, `INSERT INTO goal_tasks(id,account_id,user_id,creator_kind,creator_id,creator_name,name,goal,agent,settings,idempotency_key,time_limit_seconds,retention_seconds,requested_settings) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, id, a.AccountID, a.UserID, a.Kind, a.ID, a.Name, req.Name, req.Goal, req.Agent, raw, key, req.TimeLimitSeconds, req.RetentionSeconds, requestRaw)
	if err != nil {
		return "", false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO goal_task_events(task_id,kind,text) VALUES($1,'queued','Waiting for a task place and worker capacity')`, id); err != nil {
		return "", false, err
	}
	return id, false, tx.Commit()
}

const goalTaskColumns = `COALESCE(g.settings->>'taskProfile',''),g.id::text,g.name,g.goal,g.state,g.result,g.failure,g.creator_kind,g.creator_id,g.creator_name,g.agent,g.time_limit_seconds,g.retention_seconds,g.created_at,g.started_at,g.finished_at,g.delete_at,g.box_deleted_at,COALESCE(b.id::text,''),COALESCE(b.name,''),COALESCE(b.state,''),CASE WHEN g.state='queued' THEN (SELECT count(*) FROM goal_tasks q WHERE q.account_id=g.account_id AND q.state='queued' AND (q.created_at,q.id)<=(g.created_at,g.id)) ELSE 0 END`

func scanGoalTask(row interface{ Scan(...any) error }) (v1.GoalTask, error) {
	var t v1.GoalTask
	var result, failure []byte
	var box v1.GoalTaskBox
	err := row.Scan(&t.TaskProfile, &t.ID, &t.Name, &t.Goal, &t.State, &result, &failure, &t.Creator.Kind, &t.Creator.ID, &t.Creator.Name, &t.Agent, &t.TimeLimitSeconds, &t.RetentionSeconds, &t.CreatedAt, &t.StartedAt, &t.FinishedAt, &t.DeleteAt, &t.BoxDeletedAt, &box.ID, &box.Name, &box.State, &t.QueuePosition)
	if err != nil {
		return t, err
	}
	if result != nil {
		if err = json.Unmarshal(result, &t.Result); err != nil {
			return t, err
		}
	}
	if failure != nil {
		if err = json.Unmarshal(failure, &t.Failure); err != nil {
			return t, err
		}
	}
	if box.ID != "" {
		t.Box = &box
	}
	t.Links = []v1.GoalTaskLink{}
	return t, nil
}

func (s *Store) goalTask(ctx context.Context, a goalTaskActor, id string) (v1.GoalTask, error) {
	t, err := scanGoalTask(s.DB.QueryRowContext(ctx, `SELECT `+goalTaskColumns+` FROM goal_tasks g LEFT JOIN logical_boxes b ON b.id=g.logical_box_id WHERE g.account_id=$1 AND g.id::text=$2 AND ($3 OR (g.creator_kind=$4 AND g.creator_id=$5))`, a.AccountID, id, a.Role == "owner", a.Kind, a.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return t, errGoalTaskMissing
	}
	if err != nil {
		return t, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id::text,kind,expires_at FROM goal_task_links WHERE task_id=$1 AND revoked_at IS NULL AND expires_at>now() ORDER BY created_at`, t.ID)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var l v1.GoalTaskLink
		if err = rows.Scan(&l.ID, &l.Kind, &l.ExpiresAt); err != nil {
			return t, err
		}
		t.Links = append(t.Links, l)
	}
	return t, rows.Err()
}

// claimGoalTask serializes admission on the account row, shared by all creators.
// A terminal task retains its place until its worker slot is actually released.
func (s *Store) claimGoalTask(ctx context.Context, account, id string) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var locked string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM accounts WHERE id=$1 FOR UPDATE`, account).Scan(&locked); err != nil {
		return false, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM goal_tasks WHERE account_id=$1 AND slot_held`, account).Scan(&count); err != nil {
		return false, err
	}
	if count >= 4 {
		return false, nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE goal_tasks SET state='starting',slot_held=true WHERE account_id=$1 AND id=$2 AND state='queued'`, account, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO goal_task_events(task_id,kind,text) VALUES($1,'starting','Preparing workspace')`, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

type goalTaskUpload struct {
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Data      []byte `json:"data"`
}

func (s *Store) finishGoalTask(ctx context.Context, account, id, state, text, reason string, files []goalTaskUpload) (bool, error) {
	if !v1.GoalTaskFinished(state) {
		return false, fmt.Errorf("invalid task outcome")
	}
	if len(text) > 20000 || len(files) > 8 {
		return false, fmt.Errorf("result exceeds limits")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var current string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM goal_tasks WHERE account_id=$1 AND id=$2 FOR UPDATE`, account, id).Scan(&current); err != nil {
		return false, err
	}
	if v1.GoalTaskFinished(current) {
		return false, nil
	}
	var result, failure any
	if state == "completed" {
		r := v1.GoalTaskResult{Text: text, Files: []v1.GoalTaskFile{}}
		for _, f := range files {
			if len(f.Data) > 25<<20 || len(f.Name) == 0 || len(f.Name) > 255 {
				return false, fmt.Errorf("invalid result file")
			}
			fileID := uuid()
			if _, err = tx.ExecContext(ctx, `INSERT INTO goal_task_files(id,task_id,name,media_type,data) VALUES($1,$2,$3,$4,$5)`, fileID, id, f.Name, f.MediaType, f.Data); err != nil {
				return false, err
			}
			r.Files = append(r.Files, v1.GoalTaskFile{ID: fileID, Name: f.Name, MediaType: f.MediaType, Size: len(f.Data)})
		}
		result, _ = json.Marshal(r)
	} else {
		failure, _ = json.Marshal(v1.GoalTaskFailure{Message: text, Reason: reason})
	}
	if _, err = tx.ExecContext(ctx, `UPDATE goal_tasks SET state=$3,result=$4,failure=$5,finished_at=now(),delete_at=now()+retention_seconds*interval '1 second' WHERE account_id=$1 AND id=$2`, account, id, state, result, failure); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE logical_boxes SET metadata=metadata||'{"goalTaskFinished":true}'::jsonb WHERE id=(SELECT logical_box_id FROM goal_tasks WHERE id=$1)`, id); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE goal_task_links SET expires_at=LEAST(expires_at,(SELECT delete_at FROM goal_tasks WHERE id=$1)) WHERE task_id=$1`, id); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO goal_task_events(task_id,kind,text) VALUES($1,$2,$3)`, id, state, text); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func goalTaskOwner(a goalTaskActor) Principal {
	return Principal{AccountID: a.AccountID, UserID: a.UserID, Role: "owner"}
}

// Keep a stable ordering for cursors without exposing names or credentials.
func goalTaskCursor(t v1.GoalTask) string {
	return t.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + t.ID
}

func (s *Store) unclaimGoalTask(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE goal_tasks SET state='queued',slot_held=false WHERE id=$1 AND state='starting' AND logical_box_id IS NULL`, id)
	return err
}

// Save to the creator's existing chat even if it is asleep. The normal message
// reconciler delivers it when that session is active; this never allocates a box.
func (s *Store) queueGoalTaskNotice(ctx context.Context, p Principal, t v1.GoalTask, body string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO goal_task_notices(task_id,account_id,box_id,body) SELECT $1,$2,id,$3 FROM logical_boxes WHERE account_id=$2 AND id::text=$4 ON CONFLICT(task_id) DO NOTHING`, t.ID, p.AccountID, body, t.Creator.ID)
	return err
}

func (s *Store) deliverGoalTaskNotices(ctx context.Context) error {
	// One statement transfers each durable notice into exactly one existing chat.
	_, err := s.DB.ExecContext(ctx, `WITH ready AS (
 SELECT n.task_id,n.account_id,n.body,t.id AS session_id,t.user_id FROM goal_task_notices n JOIN logical_boxes b ON b.id=n.box_id AND b.account_id=n.account_id
 JOIN LATERAL (SELECT id,user_id FROM box_tasks WHERE account_id=n.account_id AND logical_box_id=n.box_id AND state='active' AND agent<>'shell' ORDER BY created_at DESC LIMIT 1) t ON true
 WHERE b.state='running' AND COALESCE(b.metadata->>'goalTaskFinished','false')<>'true' FOR UPDATE OF n SKIP LOCKED LIMIT 100
 ), inserted AS (
 INSERT INTO box_messages(id,account_id,task_id,user_id,direction,body,submit,state,idempotency_key,chat_key,thread_id)
 SELECT message_id,account_id,session_id,user_id,'system',body,true,'queued','goal-result:'||task_id::text,'task-'||task_id::text,message_id FROM (SELECT gen_random_uuid() AS message_id,ready.* FROM ready) r
 ON CONFLICT(account_id,idempotency_key) DO NOTHING
 ) DELETE FROM goal_task_notices WHERE task_id IN (SELECT task_id FROM ready)`)
	return err
}

// Profile requests are compared before resolving the current preset or credentials.
func (s *Store) replayTaskProfileRequest(ctx context.Context, a goalTaskActor, req v1.CreateGoalTaskRequest, key string) (string, bool, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return "", false, err
	}
	var id string
	var same bool
	err = s.DB.QueryRowContext(ctx, `SELECT id::text,COALESCE(requested_settings,settings)=$5::jsonb FROM goal_tasks WHERE account_id=$1 AND creator_kind=$2 AND creator_id=$3 AND idempotency_key=$4`, a.AccountID, a.Kind, a.ID, key, raw).Scan(&id, &same)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !same {
		return "", false, errGoalTaskIdempotency
	}
	return id, true, nil
}
