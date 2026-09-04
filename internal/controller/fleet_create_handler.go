package controller

import (
	"context"
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
	if request.VolumeID != "" || request.VolumeName != "" {
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
	creation, err := s.Store.BeginLogicalBoxCreation(r.Context(), p, request)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, creation.Assignment.Box)
	go func() {
		if err := s.finishLogicalBoxCreation(context.Background(), creation); err != nil {
			s.Logger.Error("background logical-box creation failed", "box", creation.Request.Name, "error", err)
		}
	}()
}
