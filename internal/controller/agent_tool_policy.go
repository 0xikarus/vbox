package controller

import (
	"context"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// EffectiveAgentToolNames returns the tools this box may see and call. The
// owner-managed allow-list is an additional restriction; typed capability
// grants still decide whether privileged admin tools are usable.
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
	// Discovering valid creation settings is part of the create grant. Existing
	// manager policies gain the companion tool without an owner migration.
	if selected["create_agent_box"] {
		selected["get_agent_box_configs"] = true
		selected["get_available_workers"] = true
	}
	// Clearing an agent's context is a narrower form of the existing restart
	// grant. Give current admin policies the companion tool automatically.
	if selected["restart_agent_box"] {
		selected["clear_agent_box_context"] = true
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
	case "create_agent_box", "get_agent_box_configs", "get_available_workers":
		return capabilities.CreateAgentBox.Enabled
	case "list_agent_boxes":
		return capabilities.ManageAgentBoxes.List
	case "get_agent_box":
		return capabilities.ManageAgentBoxes.Inspect
	case "set_agent_box_tags":
		return capabilities.ManageAgentBoxes.Tag
	case "restart_agent_box", "clear_agent_box_context":
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
