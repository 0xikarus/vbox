package controller

import (
	"context"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"net/http"
	"time"

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
	if (request.VolumeID != "" || request.VolumeName != "") && (len(request.Tools) > 0 || request.SetupScript != "") {
		writeError(w, 400, fmt.Errorf("install tools after importing and resuming a volume; import does not initialize it"))
		return
	}
	if request.VolumeID != "" || request.VolumeName != "" {
		if len(request.LoginProfiles) > 0 {
			writeError(w, 400, fmt.Errorf("saved profiles may only be provisioned when creating a new workspace"))
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
	for _, ref := range request.LoginProfiles {
		if p.Role != "owner" {
			writeError(w, 403, fmt.Errorf("only an account owner may provision saved profiles"))
			return
		}
		profile, err := s.Store.LoadLoginProfile(r.Context(), p, ref.Application, ref.Name)
		if err != nil {
			writeError(w, 400, fmt.Errorf("selected %s profile %s is unavailable", ref.Application, ref.Name))
			return
		}
		err = loginprofile.Validate(ref.Application, profile.Files, time.Now())
		for _, data := range profile.Files {
			clear(data)
		}
		if err != nil {
			writeError(w, 400, fmt.Errorf("%s/%s: %w", ref.Application, ref.Name, err))
			return
		}
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
