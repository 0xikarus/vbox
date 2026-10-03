package controller

import (
	"fmt"
	"net/http"
)

// These owner route shapes land before the storage-backed mail implementation so
// the Details UI can integrate with the final paths and response fields.
func (s *Server) ownerMailBox(w http.ResponseWriter, r *http.Request, p Principal) bool {
	if _, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("box unavailable"))
		return false
	}
	return true
}

func (s *Server) ownerMailSettings(w http.ResponseWriter, r *http.Request, p Principal) {
	if !s.ownerMailBox(w, r, p) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": false, "address": "", "subscribed": false,
		"filters": map[string]string{"sender": "", "subject": ""},
		"unread":  0, "pending": 0,
	})
}

func (s *Server) ownerMailMessages(w http.ResponseWriter, r *http.Request, p Principal) {
	if s.ownerMailBox(w, r, p) {
		writeJSON(w, http.StatusOK, map[string]any{"messages": []any{}, "nextCursor": ""})
	}
}

func (s *Server) ownerMailMessage(w http.ResponseWriter, r *http.Request, p Principal) {
	if s.ownerMailBox(w, r, p) {
		writeError(w, http.StatusNotFound, fmt.Errorf("mail message unavailable"))
	}
}

func (s *Server) ownerMailRead(w http.ResponseWriter, r *http.Request, p Principal) {
	s.ownerMailMessage(w, r, p)
}

func (s *Server) ownerMailAttachment(w http.ResponseWriter, r *http.Request, p Principal) {
	s.ownerMailMessage(w, r, p)
}

func (s *Server) ownerMailOutbox(w http.ResponseWriter, r *http.Request, p Principal) {
	if s.ownerMailBox(w, r, p) {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "nextCursor": ""})
	}
}

func (s *Server) ownerMailOutboxItem(w http.ResponseWriter, r *http.Request, p Principal) {
	if s.ownerMailBox(w, r, p) {
		writeError(w, http.StatusNotFound, fmt.Errorf("outbox item unavailable"))
	}
}

func (s *Server) ownerMailApprove(w http.ResponseWriter, r *http.Request, p Principal) {
	s.ownerMailOutboxItem(w, r, p)
}

func (s *Server) ownerMailReject(w http.ResponseWriter, r *http.Request, p Principal) {
	s.ownerMailOutboxItem(w, r, p)
}

func (s *Server) ownerMailApprovals(w http.ResponseWriter, r *http.Request, p Principal) {
	writeJSON(w, http.StatusOK, map[string]any{"pending": 0, "items": []any{}})
}
