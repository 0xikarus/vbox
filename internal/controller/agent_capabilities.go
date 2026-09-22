package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// EffectiveAgentCapabilities returns the policy attached directly to a box.
// On upgraded controllers, a box with no direct policy temporarily inherits
// the union of its legacy role grants so access is not lost before its first
// policy save.
func (s *Store) EffectiveAgentCapabilities(ctx context.Context, accountID, boxID string) (v1.AgentRoleCapabilities, error) {
	var result v1.AgentRoleCapabilities
	rows, err := s.DB.QueryContext(ctx, `SELECT permission,config FROM (
		SELECT 'agent_box_policy'::text AS permission,capabilities AS config
		FROM agent_box_policies WHERE account_id=$1 AND box_id=$2
		UNION ALL
		SELECT p.permission,p.config FROM box_role_assignments a
		JOIN agent_role_permissions p ON p.account_id=a.account_id AND p.role_id=a.role_id
		WHERE a.account_id=$1 AND a.box_id=$2 AND p.scope='allow'
		AND NOT EXISTS (SELECT 1 FROM agent_box_policies d WHERE d.account_id=$1 AND d.box_id=$2)
	) policies`, accountID, boxID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var permission string
		var config []byte
		if err := rows.Scan(&permission, &config); err != nil {
			return result, err
		}
		switch permission {
		case "agent_box_policy":
			if err := json.Unmarshal(config, &result); err != nil {
				return result, err
			}
			result.MCPTools.AllowedTools = canonicalAgentMCPTools(result.MCPTools.AllowedTools)
		case v1.RolePermissionAllContacts:
			var grant v1.AllContactsGrant
			if err := json.Unmarshal(config, &grant); err != nil {
				return result, err
			}
			result.AllContacts.Enabled = result.AllContacts.Enabled || grant.Enabled
		case v1.RolePermissionRequestMoreTime:
			var grant v1.RequestMoreTimeGrant
			if err := json.Unmarshal(config, &grant); err != nil {
				return result, err
			}
			result.RequestMoreTime.Enabled = result.RequestMoreTime.Enabled || grant.Enabled
			result.RequestMoreTime.MaxExtensionMinutes = max(result.RequestMoreTime.MaxExtensionMinutes, grant.MaxExtensionMinutes)
			result.RequestMoreTime.MaxTotalMinutes = max(result.RequestMoreTime.MaxTotalMinutes, grant.MaxTotalMinutes)
		case v1.RolePermissionQueueFollowup:
			var grant v1.QueueFollowupGrant
			if err := json.Unmarshal(config, &grant); err != nil {
				return result, err
			}
			result.QueueFollowup.Enabled = result.QueueFollowup.Enabled || grant.Enabled
			result.QueueFollowup.MaxDelayMinutes = max(result.QueueFollowup.MaxDelayMinutes, grant.MaxDelayMinutes)
			result.QueueFollowup.MaxPending = max(result.QueueFollowup.MaxPending, grant.MaxPending)
		case v1.RolePermissionCreateAgentBox:
			var grant v1.CreateAgentBoxGrant
			if err := json.Unmarshal(config, &grant); err != nil {
				return result, err
			}
			result.CreateAgentBox.Enabled = result.CreateAgentBox.Enabled || grant.Enabled
			result.CreateAgentBox.MaxBoxes = max(result.CreateAgentBox.MaxBoxes, grant.MaxBoxes)
			result.CreateAgentBox.MaxDiskGiB = max(result.CreateAgentBox.MaxDiskGiB, grant.MaxDiskGiB)
			result.CreateAgentBox.AllowedAgents = unionStrings(result.CreateAgentBox.AllowedAgents, grant.AllowedAgents)
			result.CreateAgentBox.AssignableRoleIDs = unionStrings(result.CreateAgentBox.AssignableRoleIDs, grant.AssignableRoleIDs)
		case v1.RolePermissionManageAgentBoxes:
			var grant v1.ManageAgentBoxesGrant
			if err := json.Unmarshal(config, &grant); err != nil {
				return result, err
			}
			result.ManageAgentBoxes.List = result.ManageAgentBoxes.List || grant.List
			result.ManageAgentBoxes.Inspect = result.ManageAgentBoxes.Inspect || grant.Inspect
			result.ManageAgentBoxes.Tag = result.ManageAgentBoxes.Tag || grant.Tag
			result.ManageAgentBoxes.Restart = result.ManageAgentBoxes.Restart || grant.Restart
			result.ManageAgentBoxes.Delete = result.ManageAgentBoxes.Delete || grant.Delete
		case v1.RolePermissionCreateEmail:
			var grant v1.CreateEmailAddressGrant
			if err := json.Unmarshal(config, &grant); err != nil {
				return result, err
			}
			result.CreateEmail.Enabled = result.CreateEmail.Enabled || grant.Enabled
			result.CreateEmail.MaxAddresses = max(result.CreateEmail.MaxAddresses, grant.MaxAddresses)
			result.CreateEmail.Domains = unionStrings(result.CreateEmail.Domains, grant.Domains)
			result.CreateEmail.AddressTypes = unionStrings(result.CreateEmail.AddressTypes, grant.AddressTypes)
		case v1.RolePermissionSharedChats:
			var grant v1.SharedChatsGrant
			if err := json.Unmarshal(config, &grant); err != nil {
				return result, err
			}
			result.SharedChats.Discover = result.SharedChats.Discover || grant.Discover
			result.SharedChats.Read = result.SharedChats.Read || grant.Read
			result.SharedChats.Subscribe = result.SharedChats.Subscribe || grant.Subscribe
			result.SharedChats.Create = result.SharedChats.Create || grant.Create
			result.SharedChats.Invite = result.SharedChats.Invite || grant.Invite
		case v1.RolePermissionMCPTools:
			var grant v1.MCPToolsGrant
			if err := json.Unmarshal(config, &grant); err != nil {
				return result, err
			}
			result.MCPTools.Enabled = result.MCPTools.Enabled || grant.Enabled
			result.MCPTools.AllowedTools = unionStrings(result.MCPTools.AllowedTools, canonicalAgentMCPTools(grant.AllowedTools))
		}
	}
	return result, rows.Err()
}

func validateAgentBoxPolicy(request v1.PutAgentBoxPolicyRequest) (v1.PutAgentBoxPolicyRequest, error) {
	// Created boxes intentionally start with no permissions. There are no roles
	// to assign to them; their owner can configure their own policy afterwards.
	request.Capabilities.CreateAgentBox.AssignableRoleIDs = nil
	validated, err := validateAgentRoleRequest(v1.PutAgentRoleRequest{Name: "box-policy", Capabilities: request.Capabilities})
	if err != nil {
		return request, err
	}
	request.Capabilities = validated.Capabilities
	return request, nil
}

func (s *Store) AgentBoxPolicy(ctx context.Context, p Principal, boxRef string) (v1.AgentBoxPolicy, error) {
	box, err := s.LogicalBox(ctx, p, boxRef)
	if err != nil {
		return v1.AgentBoxPolicy{}, err
	}
	capabilities, err := s.EffectiveAgentCapabilities(ctx, p.AccountID, box.ID)
	if err != nil {
		return v1.AgentBoxPolicy{}, err
	}
	policy := v1.AgentBoxPolicy{BoxID: box.ID, BoxName: box.Name, Capabilities: capabilities}
	err = s.DB.QueryRowContext(ctx, `SELECT updated_at FROM agent_box_policies WHERE account_id=$1 AND box_id=$2`, p.AccountID, box.ID).Scan(&policy.UpdatedAt)
	if err == nil {
		return policy, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return v1.AgentBoxPolicy{}, err
	}
	var legacy bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM box_role_assignments WHERE account_id=$1 AND box_id=$2)`, p.AccountID, box.ID).Scan(&legacy); err != nil {
		return v1.AgentBoxPolicy{}, err
	}
	policy.Migrated = legacy
	return policy, nil
}

func (s *Store) PutAgentBoxPolicy(ctx context.Context, p Principal, boxRef string, request v1.PutAgentBoxPolicyRequest) (v1.AgentBoxPolicy, error) {
	if p.Role != "owner" {
		return v1.AgentBoxPolicy{}, fmt.Errorf("only an account owner may manage agent permissions")
	}
	request, err := validateAgentBoxPolicy(request)
	if err != nil {
		return v1.AgentBoxPolicy{}, err
	}
	box, err := s.LogicalBox(ctx, p, boxRef)
	if err != nil {
		return v1.AgentBoxPolicy{}, err
	}
	raw, err := json.Marshal(request.Capabilities)
	if err != nil {
		return v1.AgentBoxPolicy{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return v1.AgentBoxPolicy{}, err
	}
	defer tx.Rollback()
	var updatedAt time.Time
	err = tx.QueryRowContext(ctx, `INSERT INTO agent_box_policies(account_id,box_id,capabilities,updated_by) VALUES($1,$2,$3::jsonb,$4)
		ON CONFLICT(account_id,box_id) DO UPDATE SET capabilities=excluded.capabilities,updated_by=excluded.updated_by,updated_at=now()
		RETURNING updated_at`, p.AccountID, box.ID, string(raw), p.UserID).Scan(&updatedAt)
	if err != nil {
		return v1.AgentBoxPolicy{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM box_role_assignments WHERE account_id=$1 AND box_id=$2`, p.AccountID, box.ID); err != nil {
		return v1.AgentBoxPolicy{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'agent_box_policy.update','logical_box',$3,jsonb_build_object('allowed_tools',$4::jsonb))`, p.AccountID, p.UserID, box.ID, roleIDsJSON(request.Capabilities.MCPTools.AllowedTools)); err != nil {
		return v1.AgentBoxPolicy{}, err
	}
	if err := tx.Commit(); err != nil {
		return v1.AgentBoxPolicy{}, err
	}
	return v1.AgentBoxPolicy{BoxID: box.ID, BoxName: box.Name, Capabilities: request.Capabilities, UpdatedAt: updatedAt}, nil
}

func (s *Server) agentBoxPolicyHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == http.MethodGet {
		policy, err := s.Store.AgentBoxPolicy(r.Context(), p, r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, policy)
		return
	}
	var request v1.PutAgentBoxPolicyRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	policy, err := s.Store.PutAgentBoxPolicy(r.Context(), p, r.PathValue("id"), request)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func unionStrings(left, right []string) []string {
	seen := make(map[string]bool, len(left)+len(right))
	result := make([]string, 0, len(left)+len(right))
	for _, values := range [][]string{left, right} {
		for _, value := range values {
			if value != "" && !seen[value] {
				seen[value] = true
				result = append(result, value)
			}
		}
	}
	return result
}

func requireCapability(enabled bool, name string) error {
	if !enabled {
		return fmt.Errorf("%s is not granted by this box's permissions", name)
	}
	return nil
}
