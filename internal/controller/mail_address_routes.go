package controller

import (
	"fmt"
	"net/http"
)

// Account mail address routes are registered ahead of the storage migration
// so the shared UI branch can wire its request and response shapes.
func (s *Server) ownerMailAddresses(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"addresses": []any{}})
		return
	}
	writeError(w, 501, fmt.Errorf("mail address management is not ready"))
}

func (s *Server) ownerMailAddressItem(w http.ResponseWriter, r *http.Request, p Principal) {
	writeError(w, 501, fmt.Errorf("mail address management is not ready"))
}

func (s *Server) ownerMailAccountSettings(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"keepUnknown": false})
		return
	}
	writeError(w, 501, fmt.Errorf("mail settings are not ready"))
}

func (s *Server) agentMailAddresses(w http.ResponseWriter, r *http.Request, p Principal) {
	if mailDomain() == "" {
		writeError(w, 403, fmt.Errorf("disabled: mail is not configured"))
		return
	}
	writeError(w, 501, fmt.Errorf("mail address listing is not ready"))
}
