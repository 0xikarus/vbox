package controller

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
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
	FailureReason string                `json:"failureReason,omitempty"`
	CreatedAt     time.Time             `json:"createdAt,omitempty"`
	UpdatedAt     time.Time             `json:"updatedAt,omitempty"`
}

func safeAgentManagedBox(box v1.LogicalBox) agentManagedBox {
	return agentManagedBox{
		ID: box.ID, Name: box.Name, State: box.State, DefaultAgent: box.DefaultAgent,
		Roles: box.Roles, FailureReason: box.FailureReason, CreatedAt: box.CreatedAt, UpdatedAt: box.UpdatedAt,
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
		result = append(result, safeAgentManagedBox(box))
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
	writeJSON(w, http.StatusOK, safeAgentManagedBox(box))
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
