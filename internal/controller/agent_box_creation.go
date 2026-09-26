package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func (s *Server) agentBoxCreationHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	key, err := requireIdempotency(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	creatorID := r.PathValue("id")
	var request struct {
		Name          string               `json:"name"`
		Agent         string               `json:"agent"`
		DiskGiB       int64                `json:"diskGiB"`
		SlotID        string               `json:"slotId"`
		Instructions  string               `json:"instructions"`
		LoginProfiles []v1.LoginProfileRef `json:"loginProfiles"`
		RoleIDs       []string             `json:"roleIds"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	request.Agent = strings.ToLower(strings.TrimSpace(request.Agent))
	if request.SlotID != strings.TrimSpace(request.SlotID) {
		writeError(w, 400, fmt.Errorf("slotId must be an exact worker slot ID"))
		return
	}
	if err := v1.ValidateInstructionMarkdown(request.Instructions); err != nil {
		writeError(w, 400, err)
		return
	}
	if request.DiskGiB == 0 {
		request.DiskGiB = 10
	}
	if err := validateAgentBoxProfiles(request.Agent, request.LoginProfiles); err != nil {
		writeError(w, 400, err)
		return
	}
	slices.SortFunc(request.LoginProfiles, func(a, b v1.LoginProfileRef) int {
		if cmp := strings.Compare(a.Application, b.Application); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(request.RoleIDs) > 8 {
		writeError(w, 400, fmt.Errorf("select at most eight roles"))
		return
	}
	for _, roleID := range request.RoleIDs {
		if strings.TrimSpace(roleID) != roleID || roleID == "" {
			writeError(w, 400, fmt.Errorf("role IDs must be exact and non-empty"))
			return
		}
	}
	slices.Sort(request.RoleIDs)
	for i := 1; i < len(request.RoleIDs); i++ {
		if request.RoleIDs[i] == request.RoleIDs[i-1] {
			writeError(w, 400, fmt.Errorf("role IDs must be unique"))
			return
		}
	}
	caps, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, creatorID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	grant := caps.CreateAgentBox
	if err := requireCapability(grant.Enabled, "create_agent_box"); err != nil {
		writeError(w, 403, err)
		return
	}
	if !slices.Contains(grant.AllowedAgents, request.Agent) {
		writeError(w, 403, fmt.Errorf("agent type is not granted by this box's permissions"))
		return
	}
	if request.DiskGiB < 1 || request.DiskGiB > min(int64(grant.MaxDiskGiB), 1000) {
		writeError(w, 403, fmt.Errorf("diskGiB exceeds this box's permission limit"))
		return
	}
	for _, roleID := range request.RoleIDs {
		if !slices.Contains(grant.AssignableRoleIDs, roleID) {
			writeError(w, 403, fmt.Errorf("role %q is not assignable by this box", roleID))
			return
		}
	}
	reservationID := uuid()
	roleIDsJSON, _ := json.Marshal(request.RoleIDs)
	profileJSON, _ := json.Marshal(request.LoginProfiles)
	var existingBoxID, requestedName, requestedAgent, requestedInstructions, requestedSlotID, providerName, credential, region string
	var requestedDisk int64
	var requestedRoleIDs, requestedProfiles []byte
	tx, err := s.Store.DB.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer tx.Rollback()
	// Serialize every quota decision for this creator on the creator row. The
	// reservation is inserted before this lock is released, so concurrent
	// requests cannot all observe the same remaining capacity.
	err = tx.QueryRowContext(r.Context(), `SELECT provider,provider_credential,COALESCE(metadata->>'region','') FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state='running' FOR UPDATE`, p.AccountID, creatorID).Scan(&providerName, &credential, &region)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 409, fmt.Errorf("creator box is unavailable"))
		return
	}
	if err != nil {
		writeError(w, 500, err)
		return
	}
	err = tx.QueryRowContext(r.Context(), `SELECT id::text,requested_name,requested_agent,requested_disk_gib,requested_role_ids,requested_login_profiles,requested_instructions,requested_slot_id,COALESCE(created_box_id::text,'') FROM agent_box_creations WHERE account_id=$1 AND creator_box_id=$2 AND idempotency_key=$3`, p.AccountID, creatorID, key).Scan(&reservationID, &requestedName, &requestedAgent, &requestedDisk, &requestedRoleIDs, &requestedProfiles, &requestedInstructions, &requestedSlotID, &existingBoxID)
	if err == nil {
		if !sameAgentBoxRequest(request.Name, request.Agent, request.DiskGiB, request.RoleIDs, request.LoginProfiles, request.Instructions, request.SlotID, requestedName, requestedAgent, requestedDisk, requestedRoleIDs, requestedProfiles, requestedInstructions, requestedSlotID) {
			writeError(w, 409, fmt.Errorf("idempotency key was already used with different box parameters"))
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, 500, err)
			return
		}
		if existingBoxID == "" {
			_ = s.Store.DB.QueryRowContext(r.Context(), `SELECT id::text FROM logical_boxes WHERE account_id=$1 AND name=$2`, p.AccountID, requestedName).Scan(&existingBoxID)
			if existingBoxID != "" {
				_, _ = s.Store.DB.ExecContext(r.Context(), `UPDATE agent_box_creations SET created_box_id=$3,completed_at=COALESCE(completed_at,now()) WHERE account_id=$1 AND id=$2`, p.AccountID, reservationID, existingBoxID)
			}
		}
		if existingBoxID == "" {
			writeError(w, 409, fmt.Errorf("box creation is already in progress"))
			return
		}
		box, loadErr := s.Store.LogicalBox(r.Context(), Principal{AccountID: p.AccountID, UserID: p.UserID, Role: "owner"}, existingBoxID)
		if loadErr != nil {
			writeError(w, 409, loadErr)
			return
		}
		w.Header().Set("Idempotency-Replayed", "true")
		writeJSON(w, 200, box)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 500, err)
		return
	}
	var count int
	if err := tx.QueryRowContext(r.Context(), `SELECT count(*) FROM agent_box_creations WHERE account_id=$1 AND creator_box_id=$2 AND (created_box_id IS NOT NULL OR completed_at IS NULL)`, p.AccountID, creatorID).Scan(&count); err != nil {
		writeError(w, 500, err)
		return
	}
	if count >= grant.MaxBoxes {
		writeError(w, 403, fmt.Errorf("created-box limit reached"))
		return
	}
	err = tx.QueryRowContext(r.Context(), `INSERT INTO agent_box_creations(id,account_id,creator_box_id,requested_name,requested_agent,requested_disk_gib,requested_role_ids,requested_login_profiles,requested_instructions,requested_slot_id,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9,$10,$11) ON CONFLICT(account_id,creator_box_id,idempotency_key) DO NOTHING RETURNING id::text,requested_name`, reservationID, p.AccountID, creatorID, request.Name, request.Agent, request.DiskGiB, string(roleIDsJSON), string(profileJSON), request.Instructions, request.SlotID, key).Scan(&reservationID, &requestedName)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(r.Context(), `SELECT id::text,requested_name,requested_agent,requested_disk_gib,requested_role_ids,requested_login_profiles,requested_instructions,requested_slot_id,COALESCE(created_box_id::text,'') FROM agent_box_creations WHERE account_id=$1 AND creator_box_id=$2 AND idempotency_key=$3`, p.AccountID, creatorID, key).Scan(&reservationID, &requestedName, &requestedAgent, &requestedDisk, &requestedRoleIDs, &requestedProfiles, &requestedInstructions, &requestedSlotID, &existingBoxID)
		if err != nil {
			writeError(w, 409, err)
			return
		}
		if !sameAgentBoxRequest(request.Name, request.Agent, request.DiskGiB, request.RoleIDs, request.LoginProfiles, request.Instructions, request.SlotID, requestedName, requestedAgent, requestedDisk, requestedRoleIDs, requestedProfiles, requestedInstructions, requestedSlotID) {
			writeError(w, 409, fmt.Errorf("idempotency key was already used with different box parameters"))
			return
		}
		if existingBoxID == "" {
			if err := tx.Commit(); err != nil {
				writeError(w, 500, err)
				return
			}
			_ = s.Store.DB.QueryRowContext(r.Context(), `SELECT id::text FROM logical_boxes WHERE account_id=$1 AND name=$2`, p.AccountID, requestedName).Scan(&existingBoxID)
			if existingBoxID != "" {
				_, _ = s.Store.DB.ExecContext(r.Context(), `UPDATE agent_box_creations SET created_box_id=$3,completed_at=COALESCE(completed_at,now()) WHERE account_id=$1 AND id=$2`, p.AccountID, reservationID, existingBoxID)
			}
		} else if err := tx.Commit(); err != nil {
			writeError(w, 500, err)
			return
		}
		if existingBoxID != "" {
			box, loadErr := s.Store.LogicalBox(r.Context(), Principal{AccountID: p.AccountID, UserID: p.UserID, Role: "owner"}, existingBoxID)
			if loadErr != nil {
				writeError(w, 409, loadErr)
				return
			}
			w.Header().Set("Idempotency-Replayed", "true")
			writeJSON(w, 200, box)
			return
		}
		writeError(w, 409, fmt.Errorf("box creation is already in progress"))
		return
	} else if err != nil {
		writeError(w, 409, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, 500, err)
		return
	}
	if err := s.validateBoxProfileRefs(r.Context(), p.AccountID, request.LoginProfiles); err != nil {
		_, _ = s.Store.DB.ExecContext(r.Context(), `DELETE FROM agent_box_creations WHERE account_id=$1 AND id=$2 AND created_box_id IS NULL`, p.AccountID, reservationID)
		writeError(w, 409, err)
		return
	}
	owner := Principal{AccountID: p.AccountID, UserID: p.UserID, Role: "owner", Subject: p.Subject}
	if request.SlotID != "" {
		// The chosen slot determines the new volume's region.
		region = ""
	}
	create := v1.CreateLogicalBoxRequest{Name: request.Name, Provider: providerName, ProviderCredential: credential, Region: region, SlotID: request.SlotID, DefaultAgent: request.Agent, DiskGiB: request.DiskGiB, LoginProfiles: request.LoginProfiles, RoleIDs: request.RoleIDs, AllocationRequestKey: "agent-box:" + reservationID}
	var instructionSelection *v1.InstructionSelection
	if strings.TrimSpace(request.Instructions) != "" {
		instructionSelection = &v1.InstructionSelection{Markdown: request.Instructions}
	}
	resolved, err := s.Store.resolveInstructionSelection(r.Context(), owner, instructionSelection, true)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	creation, err := s.Store.BeginLogicalBoxCreation(r.Context(), owner, create)
	if err != nil {
		_, _ = s.Store.DB.ExecContext(r.Context(), `DELETE FROM agent_box_creations WHERE account_id=$1 AND id=$2 AND created_box_id IS NULL`, p.AccountID, reservationID)
		writeError(w, 409, err)
		return
	}
	if err := s.Store.PutNewBoxInstructionSnapshot(r.Context(), owner, creation.Assignment.Box.ID, resolved, ""); err != nil {
		writeError(w, 409, fmt.Errorf("could not store instruction snapshot"))
		return
	}
	if _, err := s.Store.DB.ExecContext(r.Context(), `UPDATE agent_box_creations SET created_box_id=$3,completed_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, reservationID, creation.Assignment.Box.ID); err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, http.StatusAccepted, creation.Assignment.Box)
	go func() {
		if err := s.finishLogicalBoxCreation(context.Background(), creation); err != nil {
			s.Logger.Error("agent-created logical box failed", "box", creation.Request.Name, "error", err)
		}
	}()
}

func sameAgentBoxRequest(name, agent string, disk int64, roleIDs []string, profiles []v1.LoginProfileRef, instructions, slotID, storedName, storedAgent string, storedDisk int64, storedRoleJSON, storedProfilesJSON []byte, storedInstructions, storedSlotID string) bool {
	var storedRoleIDs []string
	var storedProfiles []v1.LoginProfileRef
	if json.Unmarshal(storedRoleJSON, &storedRoleIDs) != nil || json.Unmarshal(storedProfilesJSON, &storedProfiles) != nil {
		return false
	}
	slices.Sort(storedRoleIDs)
	return name == storedName && agent == storedAgent && disk == storedDisk && slices.Equal(roleIDs, storedRoleIDs) && slices.Equal(profiles, storedProfiles) && instructions == storedInstructions && slotID == storedSlotID
}

func validateAgentBoxProfiles(agent string, profiles []v1.LoginProfileRef) error {
	if err := validateBoxProfileSelection(profiles); err != nil {
		return err
	}
	for _, profile := range profiles {
		if profile.Application != "github" && profile.Application != agent {
			return fmt.Errorf("agent login profile must match the selected agent")
		}
		if strings.TrimSpace(profile.Name) == "" {
			return fmt.Errorf("login profile name is required")
		}
	}
	return nil
}
