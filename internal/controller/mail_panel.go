package controller

import (
	"fmt"
	"net/http"
)

// These account-wide route shapes let the mail panel integrate while the
// account-scoped queries are implemented in the next commit.
func (s *Server) ownerMailPanelMessages(w http.ResponseWriter, r *http.Request, p Principal) {
	writeJSON(w, 200, map[string]any{"messages": []any{}, "nextCursor": ""})
}
func (s *Server) ownerMailPanelMessage(w http.ResponseWriter, r *http.Request, p Principal) {
	writeError(w, 404, fmt.Errorf("mail message unavailable"))
}
func (s *Server) ownerMailPanelRead(w http.ResponseWriter, r *http.Request, p Principal) {
	writeError(w, 404, fmt.Errorf("mail message unavailable"))
}
func (s *Server) ownerMailPanelAttachment(w http.ResponseWriter, r *http.Request, p Principal) {
	writeError(w, 404, fmt.Errorf("mail attachment unavailable"))
}
func (s *Server) ownerMailPanelRelease(w http.ResponseWriter, r *http.Request, p Principal) {
	writeError(w, 404, fmt.Errorf("mail message unavailable"))
}
func (s *Server) ownerMailPanelDelete(w http.ResponseWriter, r *http.Request, p Principal) {
	writeError(w, 404, fmt.Errorf("mail message unavailable"))
}
func (s *Server) ownerMailPanelOutbox(w http.ResponseWriter, r *http.Request, p Principal) {
	writeJSON(w, 200, map[string]any{"items": []any{}, "nextCursor": ""})
}
func (s *Server) ownerMailPanelSummary(w http.ResponseWriter, r *http.Request, p Principal) {
	writeJSON(w, 200, map[string]any{"unread": 0, "quarantine": 0, "pending": 0, "boxes": []any{}})
}
