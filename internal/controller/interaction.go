package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func taskPrincipal(accountID string, task v1.BoxTask) Principal {
	return Principal{AccountID: accountID, UserID: task.UserID, Role: task.RequestedRole, Subject: "task:" + task.ID}
}

func (s *Server) executeBoxTask(ctx context.Context, accountID string, task v1.BoxTask) error {
	p := taskPrincipal(accountID, task)
	box, err := s.Store.LogicalBox(ctx, p, task.LogicalBoxID)
	if err != nil {
		_ = s.Store.SetBoxTaskState(ctx, accountID, task.ID, "failed", err.Error())
		return err
	}
	switch box.State {
	case v1.LogicalBoxDetached, v1.LogicalBoxHibernated:
		allocation, err := s.Store.ReserveAllocation(ctx, p, box.ID, "task-allocation:"+task.ID, "task:"+task.ID, 2*time.Minute)
		if err != nil {
			_ = s.Store.SetBoxTaskState(ctx, accountID, task.ID, "waiting_capacity", err.Error())
			return err
		}
		if allocation.State == "queued" {
			_ = s.Store.SetBoxTaskState(ctx, accountID, task.ID, "waiting_capacity", "")
			return nil
		}
		if allocation.State != "ready" {
			if err := s.activateAllocation(ctx, accountID, allocation, allocation.State == "attaching"); err != nil {
				_ = s.Store.SetBoxTaskState(ctx, accountID, task.ID, "waiting_capacity", err.Error())
				return err
			}
		}
		box, err = s.Store.LogicalBox(ctx, p, task.LogicalBoxID)
		if err != nil {
			return err
		}
	case v1.LogicalBoxReserved, v1.LogicalBoxAttaching:
		_ = s.Store.SetBoxTaskState(ctx, accountID, task.ID, "waiting_capacity", "")
		return nil
	}
	if box.State != v1.LogicalBoxRunning {
		err := fmt.Errorf("logical box %q is %s", box.Name, box.State)
		_ = s.Store.SetBoxTaskState(ctx, accountID, task.ID, "failed", err.Error())
		return err
	}
	claimed, err := s.Store.ClaimBoxTask(ctx, accountID, task.ID)
	if err != nil || !claimed {
		return err
	}
	message, _, found, err := s.Store.FirstQueuedTaskMessage(ctx, accountID, task.ID)
	if err != nil {
		_ = s.Store.SetBoxTaskState(ctx, accountID, task.ID, "failed", err.Error())
		return err
	}
	if !found {
		err := fmt.Errorf("task has no queued initial prompt")
		_ = s.Store.SetBoxTaskState(ctx, accountID, task.ID, "failed", err.Error())
		return err
	}
	claimedMessage, err := s.Store.ClaimBoxMessage(ctx, accountID, message.ID)
	if err != nil || !claimedMessage {
		_ = s.Store.SetBoxTaskState(ctx, accountID, task.ID, "failed", "initial prompt delivery was already claimed")
		return err
	}
	assignment, err := s.Store.assignment(ctx, accountID, box.ID)
	if err != nil {
		_ = s.Store.SetBoxMessageState(ctx, accountID, message.ID, "failed", err.Error())
		return err
	}
	prov, err := s.provider(ctx, accountID, box.Provider, box.ProviderCredential)
	if err != nil {
		_ = s.Store.SetBoxMessageState(ctx, accountID, message.ID, "failed", err.Error())
		return err
	}
	result, execErr := s.startAssignedBoxTaskRuntime(ctx, p, prov, assignment, task, message)
	if execErr != nil {
		_ = s.Store.SetBoxMessageState(ctx, accountID, message.ID, "ambiguous", execErr.Error())
		_ = s.Store.SetBoxTaskState(ctx, accountID, task.ID, "failed", "initial prompt delivery is ambiguous; inspect the terminal before retrying")
		return execErr
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		_ = s.Store.SetBoxMessageState(ctx, accountID, message.ID, "failed", detail)
		_ = s.Store.SetBoxTaskState(ctx, accountID, task.ID, "failed", detail)
		return fmt.Errorf("start tmux task exited with status %d: %s", result.ExitCode, detail)
	}
	if err := s.Store.SetBoxMessageState(ctx, accountID, message.ID, "delivered", ""); err != nil {
		return err
	}
	if err := s.Store.SetBoxTaskState(ctx, accountID, task.ID, "active", ""); err != nil {
		return err
	}
	if err := s.Store.AppendSystemBoxMessage(ctx, accountID, task.ID, "online · "+task.Agent+" is ready", task.ID+":online"); err != nil {
		return err
	}
	s.watchAgentReply(accountID, task, message)
	return nil
}

func (s *Server) startAssignedBoxTaskRuntime(ctx context.Context, p Principal, prov provider.Provider, a fleetAssignment, task v1.BoxTask, message v1.BoxMessage) (provider.ExecResult, error) {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return provider.ExecResult{}, fmt.Errorf("task assignment lock unavailable")
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM logical_boxes WHERE id=$1 AND account_id=$2 AND state='running' AND fencing_token=$3 AND assignment_generation=$4 FOR UPDATE`, a.Box.ID, p.AccountID, a.FencingToken, a.Box.AssignmentGeneration).Scan(&id)
	if err != nil {
		return provider.ExecResult{}, fmt.Errorf("task assignment changed")
	}
	if err = stageWorkspaceRuntime(ctx, prov, a.Slot.ServiceID, s.WorkerRuntime); err != nil {
		return provider.ExecResult{}, err
	}
	if err = s.provisionDesktopAgent(ctx, tx, p, a, prov); err != nil {
		return provider.ExecResult{}, err
	}
	result, err := s.startBoxTaskRuntime(ctx, p.AccountID, prov, a.Slot.ServiceID, task, message)
	if err != nil {
		return result, err
	}
	if result.ExitCode == 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE logical_boxes SET metadata=jsonb_set(jsonb_set(metadata,'{primarySession}',to_jsonb($3::text)),'{primaryAgent}',to_jsonb($4::text)),updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, a.Box.ID, task.Session, task.Agent); err != nil {
			return result, fmt.Errorf("task startup outcome uncertain; inspect its terminal")
		}
	}
	if err = tx.Commit(); err != nil {
		return result, fmt.Errorf("task startup outcome uncertain; inspect its terminal")
	}
	return result, nil
}

func (s *Server) startBoxTaskRuntime(ctx context.Context, accountID string, prov provider.Provider, serviceID string, task v1.BoxTask, message v1.BoxMessage) (provider.ExecResult, error) {
	if err := stageWorkspaceRuntime(ctx, prov, serviceID, s.WorkerRuntime); err != nil {
		return provider.ExecResult{}, fmt.Errorf("stage matching task runtime: %w", err)
	}
	text := ""
	if task.Agent == "shell" {
		var err error
		text, err = s.boxMessagePrompt(ctx, accountID, task.Agent, message)
		if err != nil {
			return provider.ExecResult{}, fmt.Errorf("prepare message images: %w", err)
		}
	}
	prompt := base64.RawURLEncoding.EncodeToString([]byte(text))
	result, err := prov.Exec(ctx, serviceID, []string{"vmbox-runtime", "tmux-task", task.Session, task.Agent, message.ID, prompt}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 || task.Agent == "shell" {
		return result, err
	}
	command := map[string]string{"claude": "chat-deliver", "codex": "chat-codex", "opencode": "chat-opencode"}[task.Agent]
	if command == "" {
		return provider.ExecResult{}, fmt.Errorf("unsupported task agent %q", task.Agent)
	}
	return s.deliverNativeAgentChat(ctx, prov, serviceID, accountID, command, task, message)
}

func (s *Server) deliverBoxMessage(ctx context.Context, p Principal, task v1.BoxTask, message v1.BoxMessage, submit bool) error {
	box, err := s.Store.LogicalBox(ctx, p, task.LogicalBoxID)
	if err != nil {
		return err
	}
	if box.State != v1.LogicalBoxRunning || task.State != "active" {
		return fmt.Errorf("task is not active on a running logical box")
	}
	claimed, err := s.Store.ClaimBoxMessage(ctx, p.AccountID, message.ID)
	if err != nil || !claimed {
		return err
	}
	settleCtx, cancelSettlement := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancelSettlement()
	assignment, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil {
		_ = s.Store.SetBoxMessageState(settleCtx, p.AccountID, message.ID, "failed", err.Error())
		return err
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		_ = s.Store.SetBoxMessageState(settleCtx, p.AccountID, message.ID, "failed", err.Error())
		return err
	}
	var result provider.ExecResult
	var execErr error
	if task.Agent == "claude" && submit {
		result, execErr = s.deliverNativeAgentChat(ctx, prov, assignment.Slot.ServiceID, p.AccountID, "chat-deliver", task, message)
	} else if task.Agent == "codex" && submit {
		result, execErr = s.deliverNativeAgentChat(ctx, prov, assignment.Slot.ServiceID, p.AccountID, "chat-codex", task, message)
	} else if task.Agent == "opencode" && submit {
		result, execErr = s.deliverNativeAgentChat(ctx, prov, assignment.Slot.ServiceID, p.AccountID, "chat-opencode", task, message)
	} else {
		text, err := s.boxMessagePrompt(ctx, p.AccountID, task.Agent, message)
		if err != nil {
			_ = s.Store.SetBoxMessageState(settleCtx, p.AccountID, message.ID, "failed", err.Error())
			return err
		}
		encoded := base64.RawURLEncoding.EncodeToString([]byte(text))
		result, execErr = prov.Exec(ctx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "tmux-message", task.Session, message.ID, encoded, strconv.FormatBool(submit), "true"}, provider.ExecOptions{})
	}
	if execErr != nil {
		_ = s.Store.SetBoxMessageState(settleCtx, p.AccountID, message.ID, "ambiguous", execErr.Error())
		return fmt.Errorf("message delivery is ambiguous; inspect the terminal before retrying: %w", execErr)
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		_ = s.Store.SetBoxMessageState(settleCtx, p.AccountID, message.ID, "failed", detail)
		return fmt.Errorf("message delivery exited with status %d: %s", result.ExitCode, detail)
	}
	if err := s.Store.SetBoxMessageState(settleCtx, p.AccountID, message.ID, "delivered", ""); err != nil {
		return err
	}
	s.watchAgentReply(p.AccountID, task, message)
	return nil
}

func (s *Server) ReconcileBoxInteractionsNow(ctx context.Context) error {
	if err := s.Store.RecoverStaleBoxMessages(ctx, time.Now().UTC().Add(-2*time.Minute)); err != nil {
		return fmt.Errorf("recover stale messages: %w", err)
	}
	tasks, err := s.Store.RunnableBoxTasks(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, value := range tasks {
		if err := s.executeBoxTask(ctx, value.AccountID, value.Task); err != nil {
			failures = append(failures, fmt.Errorf("task %s: %w", value.Task.ID, err))
		}
	}
	messages, err := s.Store.QueuedActiveBoxMessages(ctx)
	if err != nil {
		failures = append(failures, fmt.Errorf("list queued messages: %w", err))
	}
	for _, value := range messages {
		if err := s.deliverBoxMessage(ctx, taskPrincipal(value.AccountID, value.Task), value.Task, value.Message, value.Submit); err != nil {
			failures = append(failures, fmt.Errorf("message %s: %w", value.Message.ID, err))
		}
	}
	if err := s.reconcileActiveTasks(ctx); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (s *Server) reconcileActiveTasks(ctx context.Context) error {
	tasks, err := s.Store.activeBoxTasks(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, value := range tasks {
		if err := s.reconcileActiveTask(ctx, value.AccountID, value.Task); err != nil {
			failures = append(failures, fmt.Errorf("check task %s: %w", value.Task.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (s *Server) reconcileActiveTask(ctx context.Context, accountID string, task v1.BoxTask) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	box, err := s.Store.LogicalBox(ctx, taskPrincipal(accountID, task), task.LogicalBoxID)
	if err != nil || box.State != v1.LogicalBoxRunning {
		return err
	}
	assignment, err := s.Store.assignment(ctx, accountID, box.ID)
	if err != nil {
		return err
	}
	prov, err := s.provider(ctx, accountID, box.Provider, box.ProviderCredential)
	if err != nil {
		return err
	}
	missing, err := taskSessionMissing(ctx, prov, assignment.Slot.ServiceID, task.Session)
	if err != nil || !missing {
		return err
	}
	return s.Store.failMissingTask(ctx, accountID, task, box)
}

func taskSessionMissing(ctx context.Context, prov provider.Provider, serviceID, session string) (bool, error) {
	if !validSessionName(session) {
		return false, fmt.Errorf("invalid task session")
	}
	// '=' requests an exact name, never tmux's prefix match. Provider Exec runs
	// this as the workload user, using that user's tmux socket and HOME.
	result, err := prov.Exec(ctx, serviceID, []string{"tmux", "has-session", "-t", "=" + session}, provider.ExecOptions{})
	if err != nil {
		return false, err
	}
	switch result.ExitCode {
	case 0:
		return false, nil
	case 1:
		// Require a tmux absence diagnostic; permission and transport failures
		// are not evidence that a task exited.
		missing := strings.Contains(result.Stderr, "can't find session") || strings.Contains(result.Stderr, "no server running") ||
			(strings.Contains(result.Stderr, "error connecting to") && strings.Contains(result.Stderr, "No such file or directory"))
		if missing {
			return true, nil
		}
	}
	return false, fmt.Errorf("tmux session probe failed with status %d", result.ExitCode)
}

func (s *Server) createBoxTaskHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var request v1.CreateBoxTaskRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	task, reused, err := s.Store.CreateBoxTask(r.Context(), p, r.PathValue("id"), r.Header.Get("Idempotency-Key"), request)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	status := http.StatusAccepted
	if reused {
		status = http.StatusOK
	}
	writeJSON(w, status, task)
	if !reused || task.State == "queued" || task.State == "waiting_capacity" {
		go func() {
			if err := s.executeBoxTask(context.Background(), p.AccountID, task); err != nil {
				s.Logger.Error("box task start failed", "task", task.ID, "error", err)
			}
		}()
	}
}

func (s *Server) listBoxTasksHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	values, err := s.Store.ListBoxTasks(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, values)
}

func (s *Server) getBoxTaskHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	task, err := s.Store.BoxTask(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) listBoxMessagesHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	taskID := r.PathValue("id")
	values, err := s.Store.ListBoxMessages(r.Context(), p, taskID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, values)
	task, err := s.Store.BoxTask(context.WithoutCancel(r.Context()), p, taskID)
	if err != nil || task.State != "active" || task.Agent == "shell" {
		return
	}
	unanswered, err := s.Store.UnansweredBoxMessages(context.WithoutCancel(r.Context()), p, taskID)
	if err != nil {
		s.Logger.Warn("could not list unanswered box messages", "task", taskID, "error", err)
		return
	}
	for _, message := range unanswered {
		s.watchAgentReply(p.AccountID, task, message)
	}
}

func (s *Server) sendBoxMessageHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var request v1.SendBoxMessageRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	task, err := s.Store.BoxTask(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	message, reused, err := s.Store.CreateBoxMessage(r.Context(), p, task.ID, r.Header.Get("Idempotency-Key"), request)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	submit := true
	if request.Submit != nil {
		submit = *request.Submit
	}
	if !reused && task.State == "active" {
		if err := s.deliverBoxMessage(r.Context(), p, task, message, submit); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		message.State = "delivered"
	}
	writeJSON(w, http.StatusAccepted, message)
}

func (s *Server) logicalBoxConnectionHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if box.State != v1.LogicalBoxRunning {
		writeError(w, http.StatusConflict, fmt.Errorf("logical box is %s, not running", box.State))
		return
	}
	assignment, err := s.Store.assignment(r.Context(), p.AccountID, box.ID)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	prov, err := s.provider(r.Context(), p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	connection, err := prov.Connection(r.Context(), assignment.Slot.ServiceID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	deploymentID := connection.Metadata["deploymentInstanceId"]
	if !connectionMatchesAssignment(connection, assignment) {
		writeError(w, http.StatusConflict, fmt.Errorf("resolved SSH deployment does not match the fenced assignment; retry after reconciliation"))
		return
	}
	if box.Provider == "railway" && connection.Transport != directWorkerTransport && (connection.Transport != "openssh" || connection.Endpoint != deploymentID+"@ssh.railway.com") {
		writeError(w, http.StatusBadGateway, fmt.Errorf("provider returned an invalid Railway SSH endpoint"))
		return
	}
	if connection.Metadata == nil {
		connection.Metadata = make(map[string]string)
	}
	connection.Metadata["vmboxBoxName"] = box.Name
	connection.Metadata["vmboxComputeSlot"] = assignment.Slot.ServiceName
	connection.Metadata["vmboxAssignmentState"] = "running"
	connection.Metadata["vmboxConnectionHealth"] = "connected"
	connection.Metadata["vmboxProvider"] = box.Provider
	connection.Metadata["vmboxRegion"] = assignment.Slot.Region
	connection.Metadata["vmboxWorkspace"] = "/data/workspace"
	connection.Metadata["vmboxCost"] = "managed fleet slot; see provider billing"
	// Inspecting here is read-only and uses the already selected provider. It
	// enriches the SSH handoff without exposing provider credentials or making
	// the client query Railway's control plane.
	if actual, inspectErr := connectionDisplayInfo(r.Context(), prov, connection, assignment); inspectErr == nil {
		if actual.Region != "" {
			connection.Metadata["vmboxRegion"] = actual.Region
		}
		if actual.Resources.CPU > 0 {
			connection.Metadata["vmboxCPU"] = strconv.FormatFloat(actual.Resources.CPU, 'f', -1, 64)
		}
		if actual.Resources.MemoryMiB > 0 {
			connection.Metadata["vmboxMemoryMiB"] = strconv.FormatInt(actual.Resources.MemoryMiB, 10)
		}
		disk := actual.Resources.DiskGiB
		if actual.Storage != nil && actual.Storage.SizeGiB > 0 {
			disk = actual.Storage.SizeGiB
		}
		if disk > 0 {
			connection.Metadata["vmboxDiskGiB"] = strconv.FormatInt(disk, 10)
		}
	}
	session := r.URL.Query().Get("session")
	if session == "" {
		session = "vmbox"
	}
	if !validSessionName(session) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("session must contain only letters, digits, hyphen, or underscore"))
		return
	}
	writeJSON(w, http.StatusOK, v1.LogicalBoxConnection{LogicalBoxID: box.ID, BoxName: box.Name, Session: session, Connection: connection})
}

func (s *Server) terminalSnapshotHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if box.State != v1.LogicalBoxRunning {
		writeError(w, http.StatusConflict, fmt.Errorf("logical box is %s, not running", box.State))
		return
	}
	assignment, err := s.Store.assignment(r.Context(), p.AccountID, box.ID)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	prov, err := s.provider(r.Context(), p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	session := r.URL.Query().Get("session")
	if session == "" {
		session = "vmbox"
	}
	history := r.URL.Query().Get("history")
	if history == "" {
		history = "200"
	}
	result, err := prov.Exec(r.Context(), assignment.Slot.ServiceID, []string{"vmbox-runtime", "tmux-screen", session, history}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		if err == nil {
			err = fmt.Errorf("tmux screen exited with status %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
		}
		writeError(w, http.StatusConflict, err)
		return
	}
	var snapshot v1.TerminalSnapshot
	if err := json.Unmarshal([]byte(result.Stdout), &snapshot); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("decode terminal snapshot: %w", err))
		return
	}
	snapshot.BoxName = box.Name
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) terminalInputHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var request v1.TerminalInputRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	hasText, hasKeys := request.Text != "", len(request.Keys) > 0
	if hasText == hasKeys || len(request.Text) > 100_000 || len(request.Keys) > 64 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("terminal input requires either 1-100000 bytes of text or 1-64 allowlisted keys"))
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("Idempotency-Key is required"))
		return
	}
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if box.State != v1.LogicalBoxRunning {
		writeError(w, http.StatusConflict, fmt.Errorf("logical box is %s, not running", box.State))
		return
	}
	assignment, err := s.Store.assignment(r.Context(), p.AccountID, box.ID)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	prov, err := s.provider(r.Context(), p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	session := r.URL.Query().Get("session")
	if session == "" {
		session = "vmbox"
	}
	if !validSessionName(session) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("session must contain only letters, digits, hyphen, or underscore"))
		return
	}
	messageID := terminalInputMessageID(p.AccountID, box.ID, session, key)
	var argv []string
	if hasKeys {
		data, err := json.Marshal(request.Keys)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("encode terminal keys: %w", err))
			return
		}
		encoded := base64.RawURLEncoding.EncodeToString(data)
		argv = []string{"vmbox-runtime", "tmux-keys", session, messageID, encoded}
	} else {
		submit := true
		if request.Submit != nil {
			submit = *request.Submit
		}
		encoded := base64.RawURLEncoding.EncodeToString([]byte(request.Text))
		argv = []string{"vmbox-runtime", "tmux-message", session, messageID, encoded, strconv.FormatBool(submit)}
	}
	result, execErr := prov.Exec(r.Context(), assignment.Slot.ServiceID, argv, provider.ExecOptions{})
	if execErr != nil {
		writeError(w, http.StatusConflict, fmt.Errorf("terminal input delivery is ambiguous and was not retried: %w", execErr))
		return
	}
	if result.ExitCode != 0 {
		writeError(w, http.StatusConflict, fmt.Errorf("terminal input exited with status %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr)))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func terminalInputMessageID(accountID, boxID, session, key string) string {
	hash := sha256.Sum256([]byte(accountID + "\x00" + boxID + "\x00" + session + "\x00" + key))
	return "input_" + hex.EncodeToString(hash[:16])
}
