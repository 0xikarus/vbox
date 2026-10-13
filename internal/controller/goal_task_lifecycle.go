package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

const goalTaskPreamble = "This is a single-goal temporary task box. Work until the assigned goal is done, then call completed_task with the result and any deliverable workspace files. If the goal cannot be completed, call failed_task with the reason. Those tools finish the task permanently and release this workspace. Do not stop with only a chat reply. Ask the owner through chat_ask when a decision is required."

func (s *Server) startGoalTaskReconciler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			if err := s.ReconcileGoalTasksNow(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("task reconciliation failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// A database session lock keeps two controller replicas from dispatching the
// same task. Admission itself also uses an account row lock and a durable flag.
func (s *Server) ReconcileGoalTasksNow(ctx context.Context) error {
	conn, err := s.Store.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(492,1)`).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock(492,1)`)
	}()
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id::text FROM goal_tasks ORDER BY CASE WHEN state='queued' THEN 1 ELSE 0 END,created_at,id`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		taskCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
		err = s.reconcileGoalTask(taskCtx, id)
		cancel()
		if err != nil {
			s.Logger.Warn("task reconciliation will retry", "task", id, "error", err)
		}
	}
	return s.Store.deliverGoalTaskNotices(ctx)
}

func (s *Server) reconcileGoalTask(ctx context.Context, id string) error {
	var p Principal
	var raw []byte
	var slotHeld, deleteBox bool
	var captured, notified, promptSent, nudge, activeAt *time.Time
	err := s.Store.DB.QueryRowContext(ctx, `SELECT account_id::text,user_id::text,settings,slot_held,delete_box_requested,captured_at,notified_at,prompt_sent_at,nudge_at,watchdog_active_at FROM goal_tasks WHERE id=$1`, id).Scan(&p.AccountID, &p.UserID, &raw, &slotHeld, &deleteBox, &captured, &notified, &promptSent, &nudge, &activeAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	p.Role = "owner"
	task, err := s.Store.goalTask(ctx, goalTaskActor{Principal: p}, id)
	if err != nil {
		return err
	}
	var req v1.CreateGoalTaskRequest
	if err = json.Unmarshal(raw, &req); err != nil {
		return err
	}
	if v1.GoalTaskFinished(task.State) {
		return s.cleanupGoalTask(ctx, p, task, slotHeld, deleteBox, captured, notified)
	}
	if task.State == "queued" || (task.State == "starting" && task.Box == nil) {
		actor, err := s.Store.goalTaskActor(ctx, id)
		if err != nil {
			_, err = s.Store.finishGoalTask(ctx, p.AccountID, id, "failed", "Task credential is no longer available", "box_error", nil)
			return err
		}
		if actor.Kind == "box" {
			if err = validateBoxGoalTask(actor, req); err != nil {
				_, err = s.Store.finishGoalTask(ctx, p.AccountID, id, "failed", "Task creation permission changed", "box_error", nil)
				return err
			}
		}
		profiles, err := s.Store.resolveGoalTaskProfiles(ctx, actor, req)
		if err != nil {
			_, err = s.Store.finishGoalTask(ctx, p.AccountID, id, "failed", "Selected profile is no longer available", "box_error", nil)
			return err
		}
		if task.State == "queued" {
			var held int
			if err = s.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM goal_tasks WHERE account_id=$1 AND slot_held`, p.AccountID).Scan(&held); err != nil {
				return err
			}
			if held >= 4 {
				return nil
			} // Avoid polling workers while the task cap is full.
		}
		workers, err := s.Store.agentBoxPlacementCandidates(ctx, p.AccountID, "", "", req.SlotID)
		if errors.Is(err, errNoCreationSlot) || errors.Is(err, errSelectedAgentBoxWorkerUnavailable) {
			return s.Store.unclaimGoalTask(ctx, id)
		}
		if err != nil {
			return err
		}
		candidates := []agentBoxWorker{}
		for _, worker := range workers {
			if req.MemoryGiB != 0 {
				if err := s.verifyBoxMemoryPool(ctx, p.AccountID, worker.Provider, worker.ProviderCredential, &provider.BoxLimits{MemoryMiB: req.MemoryGiB * 1024, SwapMiB: *req.SwapGiB * 1024}); err != nil {
					continue
				}
			}
			if !s.goalTaskWorkerDiskFits(ctx, p.AccountID, worker, req.DiskGiB) {
				continue
			}
			candidates = append(candidates, worker)
		}
		if len(candidates) == 0 {
			return s.Store.unclaimGoalTask(ctx, id)
		}
		if task.State == "queued" {
			claimed, err := s.Store.claimGoalTask(ctx, p.AccountID, id)
			if err != nil || !claimed {
				return err
			}
		}
		// Goal metadata is bound inside the same transaction that reserves the box.
		var creation logicalBoxCreation
		for _, worker := range candidates {
			name := "task-" + req.Name
			if len(name) > 45 {
				name = name[:45]
			}
			name += "-" + strings.ReplaceAll(id, "-", "")[:8]
			create := v1.CreateLogicalBoxRequest{GoalTaskID: id, Name: name, Provider: worker.Provider, ProviderCredential: worker.ProviderCredential, SlotID: req.SlotID, DefaultAgent: req.Agent, DiskGiB: req.DiskGiB, MemoryGiB: req.MemoryGiB, SwapGiB: req.SwapGiB, DesktopResolution: req.DesktopResolution, LoginProfiles: profiles, RoleIDs: req.RoleIDs, Tools: req.Tools, AllocationRequestKey: "goal-task:" + id}
			creatorIDs := []string{}
			if task.Creator.Kind == "box" {
				creatorIDs = append(creatorIDs, task.Creator.ID)
			}
			creation, err = s.Store.BeginLogicalBoxCreation(ctx, p, create, creatorIDs...)
			if err == nil {
				break
			}
			if !errors.Is(err, errNoCreationSlot) {
				_ = s.Store.unclaimGoalTask(ctx, id)
				return err
			}
		}
		if err != nil {
			_, resetErr := s.Store.DB.ExecContext(ctx, `UPDATE goal_tasks SET state='queued',slot_held=false WHERE id=$1 AND state='starting' AND logical_box_id IS NULL`, id)
			return resetErr
		}
		go func() {
			if err := s.finishLogicalBoxCreation(context.Background(), creation); err != nil {
				s.Logger.Warn("task workspace creation will retry", "task", id, "error", err)
			}
		}()
		return nil
	}
	if task.Box == nil {
		_, err = s.Store.finishGoalTask(ctx, p.AccountID, id, "failed", "Task workspace is unavailable", "box_error", nil)
		return err
	}
	box, err := s.Store.LogicalBox(ctx, p, task.Box.ID)
	if err != nil {
		return err
	}
	if box.State == v1.LogicalBoxFailed {
		_, err = s.Store.finishGoalTask(ctx, p.AccountID, id, "failed", "Task workspace failed to start", "box_error", nil)
		return err
	}
	if box.State != v1.LogicalBoxRunning {
		if task.State == "running" && (box.State == v1.LogicalBoxHibernated || box.State == v1.LogicalBoxDeleting || box.State == v1.LogicalBoxDetached) {
			_, err = s.Store.finishGoalTask(ctx, p.AccountID, id, "failed", "Task workspace stopped before reporting a result", "box_error", nil)
			return err
		}
		return nil
	}
	if task.State == "starting" {
		_, err = s.Store.DB.ExecContext(ctx, `UPDATE goal_tasks SET state='running',started_at=COALESCE(started_at,now()),running_observed_at=now() WHERE id=$1 AND state='starting'`, id)
		if err != nil {
			return err
		}
		_, err = s.Store.DB.ExecContext(ctx, `INSERT INTO goal_task_events(task_id,kind,text) VALUES($1,'running','Agent is ready')`, id)
		if err != nil {
			return err
		}
	}
	if promptSent == nil {
		if _, err = s.routeBoxMessage(ctx, p, box.ID, "goal-task:"+id+":prompt", v1.DirectBoxMessageRequest{Agent: req.Agent, Text: req.Goal}); err != nil {
			return err
		}
		_, err = s.Store.DB.ExecContext(ctx, `UPDATE goal_tasks SET prompt_sent_at=now(),started_at=now(),running_observed_at=now() WHERE id=$1 AND prompt_sent_at IS NULL AND state='running'`, id)
		return err
	}
	var exhausted bool
	err = s.Store.DB.QueryRowContext(ctx, `UPDATE goal_tasks SET running_seconds=running_seconds+GREATEST(0,EXTRACT(EPOCH FROM now()-COALESCE(running_observed_at,now()))),running_observed_at=now() WHERE id=$1 AND state='running' RETURNING running_seconds>=time_limit_seconds`, id).Scan(&exhausted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if exhausted {
		_, err = s.Store.finishGoalTask(ctx, p.AccountID, id, "timed_out", "Task running-time limit reached", "timeout", nil)
		return err
	}
	activity, err := s.Store.BoxAgentActivity(ctx, p.AccountID, box.State, box.ID)
	if err != nil {
		return err
	}
	if activity.State == v1.AgentActivityWaiting || activity.State == v1.AgentActivityWorking {
		_, err = s.Store.DB.ExecContext(ctx, `UPDATE goal_tasks SET watchdog_active_at=now() WHERE id=$1`, id)
		return err
	}
	if activity.State != v1.AgentActivityIdle && activity.State != v1.AgentActivityStalled {
		return nil
	}
	last := *promptSent
	for _, at := range []*time.Time{activity.LastOutputAt, activity.LastAgentMessageAt, activity.LastInboundAt, activeAt} {
		if at != nil && at.After(last) {
			last = *at
		}
	}
	if nudge == nil && time.Since(last) >= 10*time.Minute {
		_, err = s.routeBoxMessage(ctx, p, box.ID, "goal-nudge:"+id+":"+last.UTC().Format(time.RFC3339Nano), v1.DirectBoxMessageRequest{Agent: req.Agent, Text: "Call completed_task or failed_task."})
		if err != nil {
			return err
		}
		_, err = s.Store.DB.ExecContext(ctx, `UPDATE goal_tasks SET nudge_at=now() WHERE id=$1 AND state='running'`, id)
		if err != nil {
			return err
		}
		_, err = s.Store.DB.ExecContext(ctx, `INSERT INTO goal_task_events(task_id,kind,text) VALUES($1,'nudge','Call completed_task or failed_task.')`, id)
		return err
	}
	if nudge != nil && time.Since(*nudge) >= 10*time.Minute && time.Since(last) >= 10*time.Minute {
		_, err = s.Store.finishGoalTask(ctx, p.AccountID, id, "abandoned", "Agent stopped without reporting a result", "abandoned", nil)
		return err
	}
	return nil
}

func (s *Server) cleanupGoalTask(ctx context.Context, p Principal, t v1.GoalTask, held, deleteBox bool, captured, notified *time.Time) error {
	var box v1.LogicalBox
	var err error
	if t.Box != nil {
		box, err = s.Store.LogicalBox(ctx, p, t.Box.ID)
		if err != nil {
			return err
		}
	}
	if captured == nil {
		transcript, err := s.Store.goalTaskTranscript(ctx, t.ID)
		if err != nil {
			return err
		}
		terminal := ""
		var at *time.Time
		if box.State == v1.LogicalBoxRunning {
			a, err := s.Store.assignment(ctx, p.AccountID, box.ID)
			if err != nil {
				return err
			}
			prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
			if err != nil {
				return err
			}
			captureCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			pixels, captureErr := readDesktopCapture(captureCtx, prov, a.Slot.ServiceID, nativeFence(a), false)
			cancel()
			if captureErr == nil {
				now := time.Now().UTC()
				at = &now
			} else {
				pixels = nil
			}
			tasks, err := s.Store.ListBoxTasks(ctx, p, box.ID)
			if err != nil {
				return err
			}
			if len(tasks) > 0 {
				terminalCtx, terminalCancel := context.WithTimeout(ctx, 8*time.Second)
				res, err := prov.Exec(terminalCtx, a.Slot.ServiceID, []string{"vmbox-runtime", "tmux-screen", tasks[0].Session, "500"}, provider.ExecOptions{})
				terminalCancel()
				if err == nil && res.ExitCode == 0 {
					var snap v1.TerminalSnapshot
					if json.Unmarshal([]byte(res.Stdout), &snap) == nil {
						terminal = snap.Content
					}
				}
			}
			current, fenceErr := s.Store.assignment(ctx, p.AccountID, box.ID)
			if fenceErr != nil || nativeFence(current) != nativeFence(a) {
				return fmt.Errorf("task workspace changed during capture")
			}

			_, err = s.Store.DB.ExecContext(ctx, `UPDATE goal_tasks SET transcript=$2,final_screenshot=$3,screenshot_at=$4,final_terminal=$5,captured_at=now() WHERE id=$1`, t.ID, []byte(transcript), pixels, at, terminal)
			if err != nil {
				return err
			}
		} else {
			_, err = s.Store.DB.ExecContext(ctx, `UPDATE goal_tasks SET transcript=$2,captured_at=now() WHERE id=$1`, t.ID, []byte(transcript))
			if err != nil {
				return err
			}
		}
	}
	if notified == nil {
		text := ""
		if t.Result != nil {
			text = t.Result.Text
		}
		if t.Failure != nil {
			text = t.Failure.Message
		}
		if t.Creator.Kind == "box" {
			if err = s.Store.queueGoalTaskNotice(ctx, p, t, "[Task "+t.State+"] "+t.Name+": "+text); err != nil {
				return err
			}

		} else {
			s.pushAccountNotification(p.AccountID, map[string]string{"title": "Task " + t.State + ": " + t.Name, "body": text, "url": "/tasks#" + t.ID, "tag": "task:" + t.ID})
		}
		_, err = s.Store.DB.ExecContext(ctx, `UPDATE goal_tasks SET notified_at=now() WHERE id=$1`, t.ID)
		if err != nil {
			return err
		}
	}
	if t.Box != nil && box.SlotID != "" {
		if box.State == v1.LogicalBoxRunning {
			a, err := s.Store.BeginLogicalBoxRelease(ctx, p, box.ID, v1.LogicalBoxHibernating)
			if err != nil {
				return err
			}
			if !a.Released {
				s.startLogicalBoxHibernate(p, box.ID)
			}
		} else if box.State == v1.LogicalBoxFailed {
			if pendingVolume(box.VolumeID) {
				return s.cancelUnmaterializedBoxCreation(ctx, p, box)
			}
			_, err = s.queueLogicalBoxDelete(ctx, p, box.ID, box.Name)
			if err == nil {
				s.startLogicalBoxDelete(p, box.ID)
			}
			return err
		}
		return nil
	}
	if held {
		if _, err = s.Store.DB.ExecContext(ctx, `UPDATE goal_tasks SET slot_held=false WHERE id=$1`, t.ID); err != nil {
			return err
		}
	}
	expired := t.DeleteAt != nil && !t.DeleteAt.After(time.Now())
	if t.Box != nil && (deleteBox || expired) {
		_, err = s.queueLogicalBoxDelete(ctx, p, box.ID, box.Name)
		if err == nil {
			s.startLogicalBoxDelete(p, box.ID)
		}
		return err
	}
	if t.Box == nil {
		if _, err = s.Store.DB.ExecContext(ctx, `UPDATE goal_tasks SET box_deleted_at=COALESCE(box_deleted_at,now()) WHERE id=$1 AND started_at IS NOT NULL`, t.ID); err != nil {
			return err
		}
		if expired {
			_, err = s.Store.DB.ExecContext(ctx, `DELETE FROM goal_tasks WHERE id=$1 AND logical_box_id IS NULL AND NOT slot_held`, t.ID)
			return err
		}
	}
	return nil
}
