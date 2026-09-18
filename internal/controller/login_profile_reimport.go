package controller

// Editing the login profiles already imported into a box. This re-runs the same
// integrity-checked credential transfer used at creation, so an owner can
// refresh Claude/Codex/OpenCode/GitHub credentials on an existing box without
// recreating it. An agent profile also selects the matching harness. While the
// box is running, stale agent sessions are stopped immediately; while it is
// hibernated or detached, the selection is queued for the next start. Managed
// instructions and ordinary workspace files are not changed.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

const maxBoxLoginProfiles = 1

// validateBoxProfileRefs accepts only profiles that exist for the account and
// validate for their application. Credential bytes are cleared immediately.
func (s *Server) validateBoxProfileRefs(ctx context.Context, accountID string, refs []v1.LoginProfileRef) error {
	// Selection shape is checked before any store read so a malformed request is
	// rejected on its own terms instead of surfacing as an unavailable profile.
	seen := map[string]bool{}
	for _, ref := range refs {
		key := ref.Application + "/" + ref.Name
		if seen[key] {
			return fmt.Errorf("login profile %s is selected twice", key)
		}
		seen[key] = true
	}
	if len(refs) > maxBoxLoginProfiles {
		return fmt.Errorf("select at most %d login profile", maxBoxLoginProfiles)
	}
	for _, ref := range refs {
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
		err = applyBoxLoginProfilesWithRollback(request.Profiles, state.Imported, func(refs []v1.LoginProfileRef) error {
			return s.applyBoxLoginProfiles(ctx, prov, assignment, refs)
		})
		if err != nil {
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
		state.Note = fmt.Sprintf("Profile applied. %s is now the box harness; stale agent conversations were closed so the next message starts with these credentials.", selectedProfileAgent(box.DefaultAgent, request.Profiles))
		writeJSON(w, http.StatusOK, state)
	case v1.LogicalBoxHibernated, v1.LogicalBoxDetached:
		encoded, err := json.Marshal(request.Profiles)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("could not encode the profile selection"))
			return
		}
		defaultAgent := selectedProfileAgent(box.DefaultAgent, request.Profiles)
		result, err := s.Store.DB.ExecContext(r.Context(), `UPDATE logical_boxes SET default_agent=$4,metadata=jsonb_set(metadata,'{pendingLoginProfiles}',$3::jsonb),updated_at=now() WHERE account_id=$1 AND id=$2 AND state IN ('hibernated','detached')`, p.AccountID, box.ID, encoded, defaultAgent)
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
		state.Note = fmt.Sprintf("Profile saved. %s will be the box harness and the credentials are applied on its next start.", defaultAgent)
		writeJSON(w, http.StatusOK, state)
	default:
		writeError(w, http.StatusConflict, fmt.Errorf("wait for the box to finish its current transition, then edit imported profiles"))
	}
}

// A credential transfer writes remote files before its provider check can run,
// while the database transaction can only roll back controller state. Reapply
// the recorded selection after a rejected replacement so disk and database do
// not describe different harnesses.
func applyBoxLoginProfilesWithRollback(requested, previous []v1.LoginProfileRef, apply func([]v1.LoginProfileRef) error) error {
	err := apply(requested)
	if err == nil {
		return nil
	}
	if rollbackErr := apply(previous); rollbackErr != nil {
		return fmt.Errorf("%w; restoring the previous profile also failed: %v", err, rollbackErr)
	}
	return err
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
	defaultAgent := selectedProfileAgent(assignment.Box.DefaultAgent, refs)
	if err := s.transferProfileFiles(ctx, tx, prov, assignment.Box.AccountID, assignment.Box.ID, assignment.Slot.ServiceID, assignment.Box.VolumeID, refs, defaultAgent); err != nil {
		return err
	}
	if err := reconcileBoxAgentProfile(ctx, tx, prov, assignment, defaultAgent); err != nil {
		return err
	}
	return tx.Commit()
}

func reconcileBoxAgentProfile(ctx context.Context, tx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, prov provider.Provider, assignment fleetAssignment, defaultAgent string) error {
	result, err := prov.Exec(ctx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "agent-reconcile", defaultAgent}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("could not stop stale agent sessions after changing credentials")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE box_messages SET state='failed',updated_at=now() WHERE account_id=$1 AND task_id IN (SELECT id FROM box_tasks WHERE account_id=$1 AND logical_box_id=$2 AND state IN ('queued','waiting_capacity','starting','active')) AND state='queued'`, assignment.Box.AccountID, assignment.Box.ID); err != nil {
		return fmt.Errorf("could not invalidate stale agent messages")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE box_tasks SET state='failed',failure_reason='agent profile changed; start a new conversation',updated_at=now() WHERE account_id=$1 AND logical_box_id=$2 AND state IN ('queued','waiting_capacity','starting','active')`, assignment.Box.AccountID, assignment.Box.ID); err != nil {
		return fmt.Errorf("could not invalidate stale agent conversations")
	}
	return nil
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
	defaultAgent := selectedProfileAgent(assignment.Box.DefaultAgent, state.Pending)
	if err := s.transferProfileFiles(transferCtx, tx, prov, accountID, assignment.Box.ID, assignment.Slot.ServiceID, assignment.Box.VolumeID, state.Pending, defaultAgent); err != nil {
		return err
	}
	if err := reconcileBoxAgentProfile(transferCtx, tx, prov, assignment, defaultAgent); err != nil {
		return err
	}
	return tx.Commit()
}
