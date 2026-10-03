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
	// Wake and context reset are companions to the existing restart grant.
	// Current admin policies gain them without an owner migration.
	if selected["restart_agent_box"] {
		selected["wake_agent_box"] = true
		selected["clear_agent_box_context"] = true
		selected["compact_agent_box_context"] = true
	}
	readMail := []string{"list_mail_addresses", "list_emails", "read_email", "search_emails", "mark_email_read", "download_email_attachment", "subscribe_inbox", "unsubscribe_inbox"}
	for _, name := range readMail {
		if selected[name] {
			for _, companion := range readMail {
				selected[companion] = true
			}
			break
		}
	}
	if selected["send_email"] {
		selected["list_outbox"] = true
		selected["get_outbox_status"] = true
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
	case "get_agent_box_screenshot":
		return capabilities.ManageAgentBoxes.Inspect || capabilities.ManageAgentBoxes.Control
	case "remote_control_box":
		return capabilities.ManageAgentBoxes.Control
	case "set_agent_box_tags":
		return capabilities.ManageAgentBoxes.Tag
	case "set_agent_box_run_budget", "restart_agent_box", "wake_agent_box", "clear_agent_box_context", "compact_agent_box_context":
		return capabilities.ManageAgentBoxes.Restart
	case "delete_agent_box":
		return capabilities.ManageAgentBoxes.Delete
	case "list_mail_addresses", "list_emails", "read_email", "search_emails", "mark_email_read", "download_email_attachment", "subscribe_inbox", "unsubscribe_inbox":
		return capabilities.Mail.Read
	case "send_email", "list_outbox", "get_outbox_status":
		return capabilities.Mail.Compose
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
