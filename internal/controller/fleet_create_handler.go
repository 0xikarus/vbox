package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func (s *Server) createLogicalBoxHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var request v1.CreateLogicalBoxRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	request.Normalize()
	if err := v1.ValidateSetupScript(request.SetupScript); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := v1.ValidateTools(request.Tools); err != nil {
		writeError(w, 400, err)
		return
	}
	if request.Instructions != nil {
		if err := request.Instructions.Validate(); err != nil {
			writeError(w, 400, err)
			return
		}
	}
	if (request.VolumeID != "" || request.VolumeName != "") && (len(request.Tools) > 0 || request.SetupScript != "") {
		writeError(w, 400, fmt.Errorf("install tools after importing and resuming a volume; import does not initialize it"))
		return
	}
	if request.VolumeID != "" || request.VolumeName != "" {
		if customBoxMemory(request) {
			writeError(w, 400, fmt.Errorf("memory and swap limits cannot be set while importing a volume"))
			return
		}
		if len(request.LoginProfiles) > 0 {
			writeError(w, 400, fmt.Errorf("saved profiles may only be provisioned when creating a new workspace"))
			return
		}
		if request.Instructions != nil {
			writeError(w, 400, fmt.Errorf("managed instructions may only be selected when creating a new workspace"))
			return
		}
		if p.Role != "owner" {
			writeError(w, http.StatusForbidden, fmt.Errorf("only an account owner may import an existing volume"))
			return
		}
		if request.VolumeID == "" || request.VolumeName == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("volumeId and volumeName must be provided together"))
			return
		}
		box, err := s.Store.UpsertLogicalBox(r.Context(), p, v1.LogicalBox{Name: request.Name, Provider: request.Provider, ProviderCredential: request.ProviderCredential, State: v1.LogicalBoxDetached, VolumeID: request.VolumeID, VolumeName: request.VolumeName})
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusCreated, box)
		return
	}
	if len(request.LoginProfiles) > 0 && p.Role != "owner" {
		writeError(w, 403, fmt.Errorf("only an account owner may provision saved profiles"))
		return
	}
	if err := s.validateBoxProfileRefs(r.Context(), p.AccountID, request.LoginProfiles); err != nil {
		writeError(w, 400, err)
		return
	}
	request.DefaultAgent = selectedProfileAgent(request.DefaultAgent, request.LoginProfiles)
	if customBoxMemory(request) {
		if err := s.verifyBoxMemoryPool(r.Context(), p.AccountID, request.Provider, request.ProviderCredential); err != nil {
			writeError(w, 400, err)
			return
		}
	}
	// Resolve the instruction selection against account presets and default
	// before the box exists: the snapshot is stored with the box so later
	// preset edits or deletion cannot change what this box received.
	resolvedInstructions, err := s.Store.resolveInstructionSelection(r.Context(), p, request.Instructions, true)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	toolGuidance := newBoxToolGuidance(request.Tools)
	if _, err := composeInstructionMarkdown(resolvedInstructions.Markdown, toolGuidance); err != nil {
		writeError(w, 400, fmt.Errorf("selected instructions and tool guidance: %w", err))
		return
	}
	creation, err := s.Store.BeginLogicalBoxCreation(r.Context(), p, request)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, errAgentCLIRegistryUnavailable) {
			status = http.StatusBadGateway
		}
		writeError(w, status, err)
		return
	}
	if err := s.Store.PutNewBoxInstructionSnapshot(r.Context(), p, creation.Assignment.Box.ID, resolvedInstructions, toolGuidance); err != nil {
		writeError(w, http.StatusConflict, fmt.Errorf("could not store the instruction snapshot"))
		return
	}
	writeJSON(w, http.StatusAccepted, creation.Assignment.Box)
	go func() {
		if err := s.finishLogicalBoxCreation(context.Background(), creation); err != nil {
			s.Logger.Error("background logical-box creation failed", "box", creation.Request.Name, "error", err)
		}
	}()
}
