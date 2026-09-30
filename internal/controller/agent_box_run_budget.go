package controller

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func validateAgentBoxRunBudget(actorID string, box v1.LogicalBox, protected bool, confirmation string) error {
	if box.ID == actorID {
		return fmt.Errorf("an agent box cannot change its own run-time budget")
	}
	if confirmation != box.Name {
		return fmt.Errorf("run-time budget confirmation must exactly match logical box name %q", box.Name)
	}
	if protected {
		return fmt.Errorf("protected boxes cannot have their run-time budget changed by an agent")
	}
	return nil
}

func (s *Server) agentBoxRunBudgetHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	actorID := agentBoxID(p)
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, actorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	allowed := capabilities.ManageAgentBoxes.Restart && capabilities.MCPTools.Enabled && slices.Contains(capabilities.MCPTools.AllowedTools, "set_agent_box_run_budget")
	if err := requireCapability(allowed, "set_agent_box_run_budget"); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var request struct {
		Confirmation string `json:"confirmation"`
		Seconds      *int64 `json:"seconds"`
	}
	if err := decodeJSON(r, &request); err != nil || request.Seconds == nil || *request.Seconds < 0 || *request.Seconds > maxAgentRunBudgetSeconds || (*request.Seconds > 0 && *request.Seconds < 60) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("choose 0 to disable or 60–%d seconds", maxAgentRunBudgetSeconds))
		return
	}
	targetRef := strings.TrimSpace(r.PathValue("box"))
	if targetRef == "" || request.Confirmation == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("box and exact name confirmation are required"))
		return
	}
	owner := ownerPrincipal(p)
	box, err := s.Store.LogicalBox(r.Context(), owner, targetRef)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("box unavailable"))
		return
	}
	protected, err := s.Store.BoxProtection(r.Context(), owner, box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := validateAgentBoxRunBudget(actorID, box, protected, request.Confirmation); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err := s.Store.SetBoxRunBudget(r.Context(), p.AccountID, box.ID, *request.Seconds); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	budget, err := s.Store.syncAgentRunBudget(r.Context(), p.AccountID, box.ID, s.DefaultRunBudget)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("run-time budget updated, but current status is unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"id": box.ID, "name": box.Name, "budget": budget})
}
