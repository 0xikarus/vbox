package controller

import (
	"context"
	"encoding/json"
	"fmt"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// EffectiveAgentCapabilities unions every explicitly assigned role. Boolean
// grants are additive, numeric limits take the largest configured ceiling,
// and allow-lists are unioned. Role names never influence authorization.
func (s *Store) EffectiveAgentCapabilities(ctx context.Context, accountID, boxID string) (v1.AgentRoleCapabilities, error) {
	var result v1.AgentRoleCapabilities
	rows, err := s.DB.QueryContext(ctx, `SELECT p.permission,p.config FROM box_role_assignments a
		JOIN agent_role_permissions p ON p.account_id=a.account_id AND p.role_id=a.role_id
		WHERE a.account_id=$1 AND a.box_id=$2 AND p.scope='allow' AND p.permission<>'contacts'`, accountID, boxID)
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
		return fmt.Errorf("%s is not granted by this box's assigned roles", name)
	}
	return nil
}
