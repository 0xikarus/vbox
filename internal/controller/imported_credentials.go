package controller

import (
	"encoding/json"
	"fmt"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// Only project profile references, never the encrypted profile payload or other
// creation metadata (which can contain a private setup script).
func (s *Server) importedCredentials(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	var raw []byte
	var verified bool
	err = s.Store.DB.QueryRowContext(r.Context(), `SELECT COALESCE(metadata->'importedLoginProfiles',metadata->'loginProfiles','[]'::jsonb),metadata ? 'importedLoginProfiles' FROM logical_boxes WHERE account_id=$1 AND id=$2`, p.AccountID, box.ID).Scan(&raw, &verified)
	profiles := []v1.LoginProfileRef{}
	if err != nil || json.Unmarshal(raw, &profiles) != nil {
		writeError(w, 500, fmt.Errorf("credential references unavailable"))
		return
	}
	if profiles == nil {
		profiles = []v1.LoginProfileRef{}
	}
	writeJSON(w, 200, map[string]any{"profiles": profiles, "verified": verified})
}
