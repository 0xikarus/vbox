package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// agentManagedBox deliberately omits provider credentials, volume identifiers,
// lease data, and desktop/terminal connection information.
type agentManagedBox struct {
	ID            string                `json:"id"`
	Name          string                `json:"name"`
	State         v1.LogicalBoxState    `json:"state"`
	DefaultAgent  string                `json:"defaultAgent"`
	Roles         []v1.AgentRoleSummary `json:"roles"`
	Tags          []string              `json:"tags"`
	FailureReason string                `json:"failureReason,omitempty"`
	CreatedAt     time.Time             `json:"createdAt,omitempty"`
	UpdatedAt     time.Time             `json:"updatedAt,omitempty"`
}

func safeAgentManagedBox(box v1.LogicalBox, tags ...[]string) agentManagedBox {
	labels := []string{}
	if len(tags) > 0 {
		labels = tags[0]
	}
	return agentManagedBox{
		ID: box.ID, Name: box.Name, State: box.State, DefaultAgent: box.DefaultAgent,
		Roles: box.Roles, Tags: labels, FailureReason: box.FailureReason, CreatedAt: box.CreatedAt, UpdatedAt: box.UpdatedAt,
	}
}

func agentBoxID(p Principal) string {
	return strings.TrimPrefix(p.Subject, "desktop-box:")
}

func ownerPrincipal(p Principal) Principal {
	p.Role = "owner"
	return p
}

func validateAgentBoxDeletion(actorID string, box v1.LogicalBox, protected bool, confirmation string) error {
	if box.ID == actorID {
		return fmt.Errorf("an agent box cannot delete itself")
	}
	if confirmation != box.Name {
		return fmt.Errorf("deletion confirmation must exactly match logical box name %q", box.Name)
	}
	if protected {
		return fmt.Errorf("protected boxes cannot be deleted by an agent")
	}
	return nil
}

func validateAgentBoxRestart(actorID string, box v1.LogicalBox, protected bool, confirmation string) error {
	if box.ID == actorID {
		return fmt.Errorf("an agent box cannot restart itself")
	}
	if confirmation != box.Name {
		return fmt.Errorf("restart confirmation must exactly match logical box name %q", box.Name)
	}
	if protected {
		return fmt.Errorf("protected boxes cannot be restarted by an agent")
	}
	if box.State != v1.LogicalBoxRunning {
		return fmt.Errorf("box is %s; only a running box can be restarted", box.State)
	}
	return nil
}

func validateAgentBoxWake(actorID string, box v1.LogicalBox, protected bool, confirmation string) error {
	if err := validateAgentBoxWakeTarget(actorID, box, protected, confirmation); err != nil {
		return err
	}
	if box.State != v1.LogicalBoxHibernated {
		return fmt.Errorf("box is %s; only a hibernated box can be woken", box.State)
	}
	return nil
}

func validateAgentBoxWakeTarget(actorID string, box v1.LogicalBox, protected bool, confirmation string) error {
	if box.ID == actorID {
		return fmt.Errorf("an agent box cannot wake itself")
	}
	if confirmation != box.Name {
		return fmt.Errorf("wake confirmation must exactly match logical box name %q", box.Name)
	}
	if protected {
		return fmt.Errorf("protected boxes cannot be woken by an agent")
	}
	return nil
}

func validateAgentBoxContextClear(actorID string, box v1.LogicalBox, protected bool, confirmation string) error {
	if box.ID == actorID {
		return fmt.Errorf("an agent box cannot clear its own context")
	}
	if confirmation != box.Name {
		return fmt.Errorf("context-clear confirmation must exactly match logical box name %q", box.Name)
	}
	if protected {
		return fmt.Errorf("protected boxes cannot have their context cleared by an agent")
	}
	if box.State != v1.LogicalBoxRunning {
		return fmt.Errorf("box is %s; only a running box can have its context cleared", box.State)
	}
	return nil
}

func validateAgentBoxCompact(actorID string, box v1.LogicalBox, protected bool, confirmation string) error {
	if box.ID == actorID {
		return fmt.Errorf("an agent box cannot compact its own context")
	}
	if confirmation != box.Name {
		return fmt.Errorf("compaction confirmation must exactly match logical box name %q", box.Name)
	}
	if protected {
		return fmt.Errorf("protected boxes cannot have their context compacted by an agent")
	}
	if box.State != v1.LogicalBoxRunning {
		return fmt.Errorf("box is %s; only a running box can have its context compacted", box.State)
	}
	return nil
}

func (s *Server) agentBoxesHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, agentBoxID(p))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := requireCapability(capabilities.ManageAgentBoxes.List, "list_agent_boxes"); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	boxes, err := s.Store.ListLogicalBoxes(r.Context(), ownerPrincipal(p), "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	result := make([]agentManagedBox, 0, len(boxes))
	for _, box := range boxes {
		tags, err := s.Store.BoxTags(r.Context(), ownerPrincipal(p), box.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		result = append(result, safeAgentManagedBox(box, tags))
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) agentBoxHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, agentBoxID(p))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := requireCapability(capabilities.ManageAgentBoxes.Inspect, "get_agent_box"); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	box, err := s.Store.LogicalBox(r.Context(), ownerPrincipal(p), r.PathValue("box"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	tags, err := s.Store.BoxTags(r.Context(), ownerPrincipal(p), box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, safeAgentManagedBox(box, tags))
}

// agentBoxScreenshotHandler grants a current image, never a desktop connection
// or control channel. The owner screenshot path keeps its worker and assignment
// fences, and does not wake or start the target desktop.
func (s *Server) agentBoxScreenshotHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	actorID := agentBoxID(p)
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, actorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	allowed := (capabilities.ManageAgentBoxes.Inspect && capabilities.MCPTools.Enabled && slices.Contains(capabilities.MCPTools.AllowedTools, "get_agent_box_screenshot")) || (capabilities.ManageAgentBoxes.Control && capabilities.MCPTools.Enabled && slices.Contains(capabilities.MCPTools.AllowedTools, "remote_control_box"))
	if err := requireCapability(allowed, "get_agent_box_screenshot"); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	owner := ownerPrincipal(p)
	box, err := s.Store.LogicalBox(r.Context(), owner, r.PathValue("box"))
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("box unavailable"))
		return
	}
	if box.ID == actorID {
		writeError(w, http.StatusForbidden, fmt.Errorf("use take_screenshot for this box's own desktop"))
		return
	}
	protected, err := s.Store.BoxProtection(r.Context(), owner, box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if protected {
		writeError(w, http.StatusForbidden, fmt.Errorf("protected boxes cannot be inspected by an agent"))
		return
	}
	r.SetPathValue("id", box.ID)
	s.desktopScreenshot(w, r, owner)
}

func (s *Server) agentBoxRestartHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	actorID := agentBoxID(p)
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, actorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := requireCapability(capabilities.ManageAgentBoxes.Restart, "restart_agent_box"); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	key, err := requireIdempotency(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var request struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	targetRef := strings.TrimSpace(r.PathValue("box"))
	request.Confirmation = strings.TrimSpace(request.Confirmation)
	if targetRef == "" || request.Confirmation == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("box and exact name confirmation are required"))
		return
	}
	var recordID, savedRef, savedConfirmation, targetID, targetName, state string
	err = s.Store.DB.QueryRowContext(r.Context(), `SELECT id::text,target_ref,confirmation,target_box_id::text,target_name,state FROM agent_box_restarts WHERE account_id=$1 AND actor_box_id=$2 AND idempotency_key=$3`, p.AccountID, actorID, key).Scan(&recordID, &savedRef, &savedConfirmation, &targetID, &targetName, &state)
	if err == nil {
		if savedRef != targetRef || savedConfirmation != request.Confirmation {
			writeError(w, http.StatusConflict, fmt.Errorf("idempotency key was already used for a different restart request"))
			return
		}
		if state != "complete" {
			s.startAgentBoxRestart(ownerPrincipal(p), recordID, targetID)
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"id": targetID, "name": targetName, "state": state})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	owner := ownerPrincipal(p)
	box, err := s.Store.LogicalBox(r.Context(), owner, targetRef)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	protected, err := s.Store.BoxProtection(r.Context(), owner, box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := validateAgentBoxRestart(actorID, box, protected, request.Confirmation); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	recordID = uuid()
	result, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO agent_box_restarts(id,account_id,actor_box_id,target_box_id,target_ref,target_name,confirmation,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(account_id,actor_box_id,idempotency_key) DO NOTHING`, recordID, p.AccountID, actorID, box.ID, targetRef, box.Name, request.Confirmation, key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if inserted, _ := result.RowsAffected(); inserted != 1 {
		writeError(w, http.StatusConflict, fmt.Errorf("restart request raced with another use of this idempotency key; retry it"))
		return
	}
	s.startAgentBoxRestart(owner, recordID, box.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": box.ID, "name": box.Name, "state": "requested"})
}

func (s *Server) agentBoxWakeHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	actorID := agentBoxID(p)
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, actorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	allowed := capabilities.MCPTools.Enabled && (slices.Contains(capabilities.MCPTools.AllowedTools, "wake_agent_box") || slices.Contains(capabilities.MCPTools.AllowedTools, "restart_agent_box"))
	if err := requireCapability(capabilities.ManageAgentBoxes.Restart && allowed, "wake_agent_box"); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	key, err := requireIdempotency(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var request struct {
		Confirmation  string `json:"confirmation"`
		SessionChoice string `json:"sessionChoice"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.SessionChoice != "" && request.SessionChoice != "restore" && request.SessionChoice != "fresh" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("sessionChoice must be restore or fresh"))
		return
	}
	targetRef := strings.TrimSpace(r.PathValue("box"))
	request.Confirmation = strings.TrimSpace(request.Confirmation)
	if targetRef == "" || request.Confirmation == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("box and exact name confirmation are required"))
		return
	}
	owner := ownerPrincipal(p)
	box, err := s.Store.LogicalBox(r.Context(), owner, targetRef)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	protected, err := s.Store.BoxProtection(r.Context(), owner, box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := validateAgentBoxWakeTarget(actorID, box, protected, request.Confirmation); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	if request.SessionChoice != "" && box.DefaultAgent != "codex" && box.DefaultAgent != "claude" && box.DefaultAgent != "opencode" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("sessionChoice requires a Codex, Claude, or OpenCode box"))
		return
	}
	// Allocation keys are account scoped. Include the actor and operation so
	// another box or owner request cannot accidentally share this retry key.
	allocationKey := "agent-wake:" + actorID + ":" + key
	if existing, err := s.Store.allocationByKey(r.Context(), p.AccountID, allocationKey); err == nil {
		if existing.LogicalBoxID != box.ID {
			writeError(w, http.StatusConflict, fmt.Errorf("idempotency key was already used for another box"))
			return
		}
		var savedChoice string
		if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT COALESCE(session_choice,'') FROM allocation_requests WHERE account_id=$1 AND id=$2`, p.AccountID, existing.RequestID).Scan(&savedChoice); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if savedChoice != request.SessionChoice {
			writeError(w, http.StatusConflict, fmt.Errorf("idempotency key was already used with another session choice"))
			return
		}
		s.startAgentBoxWakeAllocation(owner, existing, allocationKey)
		writeJSON(w, http.StatusAccepted, map[string]any{"id": box.ID, "name": box.Name, "state": existing.State, "sessionChoice": savedChoice})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := validateAgentBoxWake(actorID, box, protected, request.Confirmation); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	allocation, err := s.Store.ReserveAllocation(r.Context(), owner, box.ID, allocationKey, "agent-wake", 0)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	if allocation.LogicalBoxID != box.ID {
		writeError(w, http.StatusConflict, fmt.Errorf("idempotency key was already used for another box"))
		return
	}
	if _, err := s.Store.DB.ExecContext(r.Context(), `UPDATE allocation_requests SET session_choice=NULLIF($3,''),updated_at=now() WHERE account_id=$1 AND id=$2 AND session_choice IS NULL`, p.AccountID, allocation.RequestID, request.SessionChoice); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var savedChoice string
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT COALESCE(session_choice,'') FROM allocation_requests WHERE account_id=$1 AND id=$2`, p.AccountID, allocation.RequestID).Scan(&savedChoice); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if savedChoice != request.SessionChoice {
		writeError(w, http.StatusConflict, fmt.Errorf("wake request already uses another session choice"))
		return
	}
	s.startAgentBoxWakeAllocation(owner, allocation, allocationKey)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": box.ID, "name": box.Name, "state": allocation.State, "sessionChoice": request.SessionChoice})
}

func (s *Server) startAgentBoxWakeAllocation(p Principal, allocation v1.Allocation, key string) {
	if allocation.IdempotencyKey != key {
		return
	}
	if allocation.State != "reserved" && allocation.State != "attaching" {
		return
	}
	go func() {
		if err := s.activateAllocation(context.Background(), p.AccountID, allocation, allocation.State == "attaching"); err != nil {
			s.Logger.Error("agent box wake allocation failed", "box", allocation.LogicalBoxID, "allocation", allocation.RequestID, "error", err)
		}
	}()
}

func (s *Server) agentBoxClearContextHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	actorID := agentBoxID(p)
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, actorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := requireCapability(capabilities.ManageAgentBoxes.Restart, "clear_agent_box_context"); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if _, err := requireIdempotency(r); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var request struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	targetRef := strings.TrimSpace(r.PathValue("box"))
	request.Confirmation = strings.TrimSpace(request.Confirmation)
	if targetRef == "" || request.Confirmation == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("box and exact name confirmation are required"))
		return
	}
	owner := ownerPrincipal(p)
	box, err := s.Store.LogicalBox(r.Context(), owner, targetRef)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	protected, err := s.Store.BoxProtection(r.Context(), owner, box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := validateAgentBoxContextClear(actorID, box, protected, request.Confirmation); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	// Use the owner's existing harness-specific reset path, including its
	// idempotency key and chat audit marker, after checking delegated authority.
	r.SetPathValue("id", box.ID)
	s.clearBoxContextHandler(w, r, owner)
}

func (s *Server) agentBoxCompactHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	actorID := agentBoxID(p)
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, actorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := requireCapability(capabilities.ManageAgentBoxes.Restart, "compact_agent_box_context"); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if _, err := requireIdempotency(r); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var request struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	targetRef := strings.TrimSpace(r.PathValue("box"))
	request.Confirmation = strings.TrimSpace(request.Confirmation)
	if targetRef == "" || request.Confirmation == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("box and exact name confirmation are required"))
		return
	}
	owner := ownerPrincipal(p)
	box, err := s.Store.LogicalBox(r.Context(), owner, targetRef)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	protected, err := s.Store.BoxProtection(r.Context(), owner, box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := validateAgentBoxCompact(actorID, box, protected, request.Confirmation); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	r.SetPathValue("id", box.ID)
	s.compactBoxContextHandler(w, r, owner)
}

func (s *Server) startAgentBoxRestart(p Principal, recordID, boxID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		fail := func(err error) {
			_, _ = s.Store.DB.ExecContext(context.Background(), `UPDATE agent_box_restarts SET state='failed',failure_reason=$3,updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, recordID, err.Error())
			s.Logger.Warn("agent box restart stopped", "box", boxID, "error", err)
		}
		var phase string
		if err := s.Store.DB.QueryRowContext(ctx, `SELECT state FROM agent_box_restarts WHERE account_id=$1 AND id=$2`, p.AccountID, recordID).Scan(&phase); err != nil {
			fail(err)
			return
		}
		if phase == "complete" {
			return
		}
		box, err := s.Store.LogicalBox(ctx, p, boxID)
		if err != nil {
			fail(err)
			return
		}
		if phase == "allocating" && box.State == v1.LogicalBoxRunning {
			_, _ = s.Store.DB.ExecContext(ctx, `UPDATE agent_box_restarts SET state='complete',failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, recordID)
			return
		}
		if box.State == v1.LogicalBoxRunning {
			if _, err := s.Store.DB.ExecContext(ctx, `UPDATE agent_box_restarts SET state='hibernating',failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, recordID); err != nil {
				fail(err)
				return
			}
			if _, err := s.Store.BeginLogicalBoxRelease(ctx, p, box.ID, v1.LogicalBoxHibernating); err != nil {
				fail(err)
				return
			}
		}
		if err := s.resumeLogicalBoxHibernate(ctx, p, box.ID); err != nil {
			fail(err)
			return
		}
		if _, err := s.Store.DB.ExecContext(ctx, `UPDATE agent_box_restarts SET state='allocating',failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, recordID); err != nil {
			fail(err)
			return
		}
		allocation, err := s.Store.ReserveAllocation(ctx, p, box.ID, "agent-restart-"+recordID, "agent-restart", 0)
		if err != nil {
			fail(err)
			return
		}
		if (allocation.State == "reserved" || allocation.State == "attaching") && allocation.IdempotencyKey == "agent-restart-"+recordID {
			if err := s.activateAllocation(ctx, p.AccountID, allocation, allocation.State == "attaching"); err != nil {
				fail(err)
				return
			}
		}
		_, _ = s.Store.DB.ExecContext(ctx, `UPDATE agent_box_restarts SET state='complete',failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, recordID)
	}()
}

func (s *Server) agentBoxDeleteHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	actorID := agentBoxID(p)
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, actorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := requireCapability(capabilities.ManageAgentBoxes.Delete, "delete_agent_box"); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	key, err := requireIdempotency(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var request struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	targetRef := strings.TrimSpace(r.PathValue("box"))
	request.Confirmation = strings.TrimSpace(request.Confirmation)
	if targetRef == "" || request.Confirmation == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("box and exact name confirmation are required"))
		return
	}

	var savedRef, savedConfirmation, savedID, savedName string
	var accepted bool
	loadReservation := func() (bool, error) {
		err := s.Store.DB.QueryRowContext(r.Context(), `SELECT target_ref,confirmation,COALESCE(target_box_id::text,''),COALESCE(target_name,''),accepted FROM agent_box_deletions WHERE account_id=$1 AND actor_box_id=$2 AND idempotency_key=$3`, p.AccountID, actorID, key).Scan(&savedRef, &savedConfirmation, &savedID, &savedName, &accepted)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	}
	found, err := loadReservation()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if found {
		if savedRef != targetRef || savedConfirmation != request.Confirmation {
			writeError(w, http.StatusConflict, fmt.Errorf("idempotency key was already used for a different deletion request"))
			return
		}
		if accepted {
			writeJSON(w, http.StatusAccepted, map[string]any{"id": savedID, "name": savedName, "state": v1.LogicalBoxDeleting})
			return
		}
	}

	owner := ownerPrincipal(p)
	lookup := targetRef
	if savedID != "" {
		lookup = savedID
	}
	box, err := s.Store.LogicalBox(r.Context(), owner, lookup)
	if err != nil {
		// A crash after queueing may leave the reservation unmarked even though
		// the asynchronous deletion has already completed.
		if savedID != "" && savedName == request.Confirmation {
			_, _ = s.Store.DB.ExecContext(r.Context(), `UPDATE agent_box_deletions SET accepted=true,updated_at=now() WHERE account_id=$1 AND actor_box_id=$2 AND idempotency_key=$3`, p.AccountID, actorID, key)
			writeJSON(w, http.StatusAccepted, map[string]any{"id": savedID, "name": savedName, "state": v1.LogicalBoxDeleting})
			return
		}
		writeError(w, http.StatusNotFound, err)
		return
	}
	protected, err := s.Store.BoxProtection(r.Context(), owner, box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := validateAgentBoxDeletion(actorID, box, protected, request.Confirmation); err != nil {
		status := http.StatusConflict
		if box.ID == actorID || protected {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	if !found {
		result, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO agent_box_deletions(account_id,actor_box_id,idempotency_key,target_ref,confirmation,target_box_id,target_name) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(account_id,actor_box_id,idempotency_key) DO NOTHING`, p.AccountID, actorID, key, targetRef, request.Confirmation, box.ID, box.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if inserted, _ := result.RowsAffected(); inserted == 0 {
			found, err = loadReservation()
			if err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Errorf("load concurrent deletion reservation: %w", err))
				return
			}
			if !found {
				writeError(w, http.StatusInternalServerError, fmt.Errorf("concurrent deletion reservation disappeared"))
				return
			}
			if savedRef != targetRef || savedConfirmation != request.Confirmation || savedID != box.ID {
				writeError(w, http.StatusConflict, fmt.Errorf("idempotency key was already used for a different deletion request"))
				return
			}
			if accepted {
				writeJSON(w, http.StatusAccepted, map[string]any{"id": savedID, "name": savedName, "state": v1.LogicalBoxDeleting})
				return
			}
		}
	}
	box, err = s.queueLogicalBoxDelete(r.Context(), owner, box.ID, request.Confirmation)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	if _, err = s.Store.DB.ExecContext(r.Context(), `UPDATE agent_box_deletions SET accepted=true,updated_at=now() WHERE account_id=$1 AND actor_box_id=$2 AND idempotency_key=$3`, p.AccountID, actorID, key); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.startLogicalBoxDelete(owner, box.ID)
	writeJSON(w, http.StatusAccepted, safeAgentManagedBox(box))
}
