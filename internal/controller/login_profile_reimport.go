package controller

// Editing the login profiles already imported into a box. This re-runs the same
// integrity-checked credential transfer used at creation, so an owner can
// refresh Claude/Codex/OpenCode/GitHub credentials on an existing box without
// recreating it. While the box is running the credentials are written
// immediately; while it is hibernated or detached the selection is queued as
// pending and provisioned on the next start. A credential transfer never
// touches managed instructions or any other box configuration.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

const maxBoxLoginProfiles = 8

// validateBoxProfileRefs accepts only profiles that exist for the account and
// validate for their application. Credential bytes are cleared immediately.
func (s *Server) validateBoxProfileRefs(ctx context.Context, accountID string, refs []v1.LoginProfileRef) error {
	if len(refs) > maxBoxLoginProfiles {
		return fmt.Errorf("select at most %d login profiles", maxBoxLoginProfiles)
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		key := ref.Application + "/" + ref.Name
		if seen[key] {
			return fmt.Errorf("login profile %s is selected twice", key)
		}
		seen[key] = true
		profile, err := s.Store.LoadLoginProfile(ctx, Principal{AccountID: accountID}, ref.Application, ref.Name)
		if err != nil {
			return fmt.Errorf("selected %s profile %s is unavailable", ref.Application, ref.Name)
		}
		validationErr := loginprofile.Validate(ref.Application, profile.Files, time.Now())
		for _, data := range profile.Files {
			clear(data)
		}
		if validationErr != nil {
			return fmt.Errorf("%s/%s: %w", ref.Application, ref.Name, validationErr)
		}
	}
	return nil
}

// boxLoginProfileState reads the recorded references. An absent key means the
// box was created before references were recorded; verified reports whether an
// explicit imported set exists.
func (s *Server) boxLoginProfileState(ctx context.Context, accountID, boxID string) (v1.BoxLoginProfiles, error) {
	state := v1.BoxLoginProfiles{Imported: []v1.LoginProfileRef{}, Pending: []v1.LoginProfileRef{}}
	var raw, pending []byte
	var verified bool
	err := s.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(metadata->'importedLoginProfiles',metadata->'loginProfiles','[]'::jsonb),metadata ? 'importedLoginProfiles',COALESCE(metadata->'pendingLoginProfiles','[]'::jsonb) FROM logical_boxes WHERE account_id=$1 AND id=$2`, accountID, boxID).Scan(&raw, &verified, &pending)
	if err != nil {
		return state, err
	}
	if json.Unmarshal(raw, &state.Imported) != nil {
		state.Imported = []v1.LoginProfileRef{}
	}
	if json.Unmarshal(pending, &state.Pending) != nil {
		state.Pending = []v1.LoginProfileRef{}
	}
	state.Verified = verified
	return state, nil
}

// putBoxLoginProfiles edits the profiles imported into an existing box.
func (s *Server) putBoxLoginProfiles(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var request v1.PutBoxLoginProfilesRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid login profile selection"))
		return
	}
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("box unavailable"))
		return
	}
	if request.Profiles == nil {
		request.Profiles = []v1.LoginProfileRef{}
	}
	if err := s.validateBoxProfileRefs(r.Context(), p.AccountID, request.Profiles); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	state, err := s.boxLoginProfileState(r.Context(), p.AccountID, box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("credential references unavailable"))
		return
	}
	switch box.State {
	case v1.LogicalBoxRunning:
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		assignment, err := s.Store.assignment(ctx, p.AccountID, box.ID)
		if err != nil || assignment.Slot.ServiceID == "" {
			writeError(w, http.StatusConflict, fmt.Errorf("box assignment is unavailable; retry when the box is running"))
			return
		}
		prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		if err := s.applyBoxLoginProfiles(ctx, prov, assignment, request.Profiles); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		current, err := s.Store.assignment(ctx, p.AccountID, box.ID)
		if err != nil || nativeFence(current) != nativeFence(assignment) {
			writeError(w, http.StatusConflict, fmt.Errorf("assignment changed while importing credentials"))
			return
		}
		state, err = s.boxLoginProfileState(ctx, p.AccountID, box.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("credential references unavailable"))
			return
		}
		state.Applied = true
		state.Note = "Credentials written into the box. Restart the box or start a new agent conversation so the agent reads them; running sessions keep their loaded credentials until then. Instructions and other box configuration are unchanged."
		writeJSON(w, http.StatusOK, state)
	case v1.LogicalBoxHibernated, v1.LogicalBoxDetached:
		encoded, err := json.Marshal(request.Profiles)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("could not encode the profile selection"))
			return
		}
		result, err := s.Store.DB.ExecContext(r.Context(), `UPDATE logical_boxes SET metadata=jsonb_set(metadata,'{pendingLoginProfiles}',$3::jsonb),updated_at=now() WHERE account_id=$1 AND id=$2 AND state IN ('hibernated','detached')`, p.AccountID, box.ID, encoded)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("could not queue the profile selection"))
			return
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			writeError(w, http.StatusConflict, fmt.Errorf("box state changed; retry when the box is stopped"))
			return
		}
		state, err = s.boxLoginProfileState(r.Context(), p.AccountID, box.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("credential references unavailable"))
			return
		}
		state.Note = "Saved. Credentials are written when the box next starts — reboot/resume it to apply. Instructions and other box configuration are unchanged."
		writeJSON(w, http.StatusOK, state)
	default:
		writeError(w, http.StatusConflict, fmt.Errorf("wait for the box to finish its current transition, then edit imported profiles"))
	}
}

// applyBoxLoginProfiles performs the locked credential transfer for a box that
// is attached right now.
func (s *Server) applyBoxLoginProfiles(ctx context.Context, prov provider.Provider, assignment fleetAssignment, refs []v1.LoginProfileRef) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not lock credential destination")
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state='running' AND slot_id=$3 AND assignment_generation=$4 AND fencing_token=$5 AND volume_id=$6 FOR UPDATE`, assignment.Box.AccountID, assignment.Box.ID, assignment.Slot.ID, assignment.Box.AssignmentGeneration, assignment.FencingToken, assignment.Box.VolumeID).Scan(&id)
	if err != nil {
		return fmt.Errorf("credential destination assignment changed")
	}
	if err := s.transferProfileFiles(ctx, tx, prov, assignment.Box.AccountID, assignment.Box.ID, assignment.Slot.ServiceID, assignment.Box.VolumeID, refs); err != nil {
		return err
	}
	return tx.Commit()
}

// provisionPendingBoxProfiles applies a queued credential selection during box
// allocation/resume, before the box is declared ready.
func (s *Server) provisionPendingBoxProfiles(ctx context.Context, prov provider.Provider, accountID string, assignment fleetAssignment) error {
	state, err := s.boxLoginProfileState(ctx, accountID, assignment.Box.ID)
	if err != nil {
		return fmt.Errorf("load pending login profiles: %w", err)
	}
	if len(state.Pending) == 0 {
		return nil
	}
	if err := s.validateBoxProfileRefs(ctx, accountID, state.Pending); err != nil {
		return fmt.Errorf("queued login profiles are no longer valid; edit imported profiles and try again: %w", err)
	}
	transferCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	tx, err := s.Store.DB.BeginTx(transferCtx, nil)
	if err != nil {
		return fmt.Errorf("could not lock credential destination")
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(transferCtx, `SELECT id::text FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state IN ('attaching','running') AND slot_id=$3 AND assignment_generation=$4 AND fencing_token=$5 AND volume_id=$6 FOR UPDATE`, accountID, assignment.Box.ID, assignment.Slot.ID, assignment.Box.AssignmentGeneration, assignment.FencingToken, assignment.Box.VolumeID).Scan(&id)
	if err != nil {
		return fmt.Errorf("credential destination assignment changed")
	}
	if err := s.transferProfileFiles(transferCtx, tx, prov, accountID, assignment.Box.ID, assignment.Slot.ServiceID, assignment.Box.VolumeID, state.Pending); err != nil {
		return err
	}
	return tx.Commit()
}
