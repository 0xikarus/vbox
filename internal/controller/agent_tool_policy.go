package controller

import (
	"context"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// EffectiveAgentToolNames returns the tools this box may see and call. The
// owner-managed allow-list is an additional restriction; typed capability
// grants still decide whether privileged coordination tools are usable.
func (s *Store) EffectiveAgentToolNames(ctx context.Context, accountID, boxID string) ([]string, error) {
	capabilities, err := s.EffectiveAgentCapabilities(ctx, accountID, boxID)
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	if capabilities.MCPTools.Enabled {
		for _, name := range capabilities.MCPTools.AllowedTools {
			selected[name] = true
		}
	}
	allowed := append([]string(nil), v1.BasicAgentMCPTools...)
	for _, name := range v1.OptionalAgentMCPTools {
		if selected[name] && toolCapabilityAllows(name, capabilities) {
			allowed = append(allowed, name)
		}
	}
	return allowed, nil
}

func toolCapabilityAllows(name string, capabilities v1.AgentRoleCapabilities) bool {
	switch name {
	case "request_more_time":
		return capabilities.RequestMoreTime.Enabled
	case "queue_followup":
		return capabilities.QueueFollowup.Enabled
	case "discover_shared_chats":
		return capabilities.SharedChats.Discover
	case "read_shared_chat", "send_shared_chat_message":
		return capabilities.SharedChats.Read
	case "subscribe_shared_chat":
		return capabilities.SharedChats.Subscribe
	case "create_shared_chat":
		return capabilities.SharedChats.Create
	case "invite_to_shared_chat":
		return capabilities.SharedChats.Invite
	case "create_email_address":
		return capabilities.CreateEmail.Enabled
	case "create_agent_box":
		return capabilities.CreateAgentBox.Enabled
	case "list_agent_boxes":
		return capabilities.ManageAgentBoxes.List
	case "get_agent_box":
		return capabilities.ManageAgentBoxes.Inspect
	case "set_agent_box_tags":
		return capabilities.ManageAgentBoxes.Tag
	case "restart_agent_box":
		return capabilities.ManageAgentBoxes.Restart
	case "delete_agent_box":
		return capabilities.ManageAgentBoxes.Delete
	default:
		return true
	}
}

func (s *Server) agentToolPolicyHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	tools, err := s.Store.EffectiveAgentToolNames(r.Context(), p.AccountID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
}
