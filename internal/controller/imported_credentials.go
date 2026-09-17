package controller

import (
	"fmt"
	"net/http"
)

// Only project profile references, never the encrypted profile payload or other
// creation metadata (which can contain a private setup script). Pending holds a
// credential selection queued for the box's next start.
func (s *Server) importedCredentials(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	state, err := s.boxLoginProfileState(r.Context(), p.AccountID, box.ID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("credential references unavailable"))
		return
	}
	writeJSON(w, 200, state)
}
