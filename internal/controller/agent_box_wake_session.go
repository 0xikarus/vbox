package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

// The choice belongs to the allocation, not to the controller goroutine that
// accepted the wake request. Queued wakes and controller restarts use the same
// record. A short claim lease allows recovery if the controller stops mid-call.
func (s *Server) applyPendingWakeSessionChoice(ctx context.Context, accountID, requestID string) {
	var boxID, choice string
	err := s.Store.DB.QueryRowContext(ctx, `UPDATE allocation_requests r SET session_choice_attempted_at=now()
		FROM logical_boxes b WHERE r.account_id=$1 AND r.id=$2 AND r.state='ready' AND r.session_choice IS NOT NULL
		AND b.id=r.logical_box_id AND b.account_id=r.account_id AND b.state='running' AND b.assignment_generation=r.assignment_generation
		AND NOT session_choice_applied AND (session_choice_attempted_at IS NULL OR session_choice_attempted_at<now()-interval '2 minutes')
		RETURNING r.logical_box_id::text,r.session_choice`, accountID, requestID).Scan(&boxID, &choice)
	if err == sql.ErrNoRows {
		return
	}
	if err != nil {
		s.Logger.Warn("wake session choice claim failed", "allocation", requestID, "error", err)
		return
	}
	applyCtx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	err = s.applyWakeSessionChoice(applyCtx, accountID, boxID, choice)
	if err != nil {
		s.Logger.Warn("wake session choice failed; it will be retried", "allocation", requestID, "box", boxID, "error", err)
	}
	_, updateErr := s.Store.DB.ExecContext(ctx, `UPDATE allocation_requests SET session_choice_applied=$3,session_choice_attempted_at=CASE WHEN $3 THEN NULL ELSE now()-interval '2 minutes' END,updated_at=now() WHERE account_id=$1 AND id=$2`, accountID, requestID, err == nil)
	if updateErr != nil {
		s.Logger.Warn("wake session choice status update failed", "allocation", requestID, "error", updateErr)
	}
}

func (s *Server) reconcilePendingWakeSessionChoices(ctx context.Context) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT r.account_id::text,r.id::text FROM allocation_requests r
		JOIN logical_boxes b ON b.id=r.logical_box_id AND b.account_id=r.account_id AND b.state='running' AND b.assignment_generation=r.assignment_generation
		WHERE r.state='ready' AND r.session_choice IS NOT NULL AND NOT r.session_choice_applied
		AND (r.session_choice_attempted_at IS NULL OR r.session_choice_attempted_at<now()-interval '2 minutes')
		ORDER BY r.updated_at LIMIT 32`)
	if err != nil {
		s.Logger.Warn("pending wake session choice lookup failed", "error", err)
		return
	}
	var pending [][2]string
	for rows.Next() {
		var entry [2]string
		if rows.Scan(&entry[0], &entry[1]) == nil {
			pending = append(pending, entry)
		}
	}
	rows.Close()
	for _, entry := range pending {
		s.applyPendingWakeSessionChoice(ctx, entry[0], entry[1])
	}
}

func (s *Server) applyWakeSessionChoice(ctx context.Context, accountID, boxID, choice string) error {
	var ownerID string
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT owner_user_id::text FROM logical_boxes WHERE account_id=$1 AND id=$2`, accountID, boxID).Scan(&ownerID); err != nil {
		return err
	}
	p := Principal{AccountID: accountID, UserID: ownerID, Role: "owner"}
	box, err := s.Store.LogicalBox(ctx, p, boxID)
	if err != nil || box.State != v1.LogicalBoxRunning {
		return fmt.Errorf("woken box is not running")
	}
	agent := box.DefaultAgent
	if agent != "codex" && agent != "claude" && agent != "opencode" {
		return fmt.Errorf("%s has no managed conversation", agent)
	}
	tasks, err := s.Store.ListBoxTasks(ctx, p, boxID)
	if err != nil {
		return err
	}
	task := reusableBoxTask(tasks, box.State, agent, "")
	if task == nil || task.State != "active" {
		return fmt.Errorf("%s chat session is not active yet", agent)
	}
	a, err := s.Store.assignment(ctx, accountID, boxID)
	if err != nil || a.Slot.ServiceID == "" {
		return fmt.Errorf("box assignment unavailable")
	}
	prov, err := s.provider(ctx, accountID, box.Provider, box.ProviderCredential)
	if err != nil {
		return err
	}
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", agent + "-resume-candidate", task.Session}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("saved %s session lookup failed", agent)
	}
	var candidate *boxruntime.ResumeCandidate
	if err := json.Unmarshal([]byte(result.Stdout), &candidate); err != nil {
		return err
	}
	if candidate == nil {
		return nil
	}
	var raw []byte
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(metadata -> $3,'null'::jsonb) FROM logical_boxes WHERE account_id=$1 AND id=$2`, accountID, boxID, agent+"ResumeDecision").Scan(&raw); err != nil {
		return err
	}
	var previous codexResumeDecision
	_ = json.Unmarshal(raw, &previous)
	if previous.SavedAt.Equal(candidate.SavedAt) {
		// The owner may have chosen in Chat while the allocation was becoming
		// ready. A recorded choice wins and ends retries for this wake request.
		return nil
	}
	var sent bool
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM box_messages m JOIN box_tasks t ON t.id=m.task_id WHERE m.account_id=$1 AND t.logical_box_id=$2 AND m.direction IN ('user','box') AND m.created_at>$3)`, accountID, boxID, candidate.SavedAt).Scan(&sent); err != nil {
		return err
	}
	if sent {
		return fmt.Errorf("new chat message arrived before the wake session choice")
	}
	if choice == "restore" {
		result, err = prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", agent + "-resume-session", task.Session, candidate.SessionID, candidate.SavedAt.Format(time.RFC3339Nano)}, provider.ExecOptions{})
		if err != nil || result.ExitCode != 0 {
			return fmt.Errorf("%s session could not be restored: %s", agent, strings.TrimSpace(result.Stderr))
		}
	}
	saved, err := s.Store.saveAgentResumeDecision(ctx, p, a, agent, *candidate, choice)
	if err != nil {
		return err
	}
	if !saved {
		return fmt.Errorf("box assignment changed during session choice")
	}
	return nil
}
