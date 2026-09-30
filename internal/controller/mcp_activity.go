package controller

import (
	"fmt"
	"net/http"
	"slices"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

// agentToolActivityHandler writes the small system bullets shown in Box Chat.
// The box sends tool metadata only; the controller builds the displayed text.
func (s *Server) agentToolActivityHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var activity boxruntime.MCPActivity
	if err := decodeJSON(r, &activity); err != nil || !chatReadyEventID.MatchString(activity.ID) ||
		!slices.Contains(v1.BasicAgentMCPTools, activity.Tool) && !slices.Contains(v1.OptionalAgentMCPTools, activity.Tool) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid MCP activity"))
		return
	}
	body := "MCP · " + activity.Tool
	if activity.Tool == "heartbeat" && (activity.Action == "start" || activity.Action == "stop") {
		body += " · " + activity.Action
	}
	if activity.Contact {
		body += " · contact"
	}
	if activity.Failed {
		body += " · failed"
	}
	boxID := r.PathValue("id") // Bound by desktopAgentAuth, never caller supplied.
	_, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO box_events(id,account_id,box_id,body,event_key)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_id,event_key) DO NOTHING`, uuid(), p.AccountID, boxID, body, "mcp:"+boxID+":"+activity.ID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("MCP activity could not be stored"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stored": true})
}
