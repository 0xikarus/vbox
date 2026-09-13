package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func (s *Server) desktopSecrets(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	box := r.PathValue("id")
	if r.Method == http.MethodGet {
		values, err := s.Store.ListDesktopSecrets(r.Context(), p, box)
		if err != nil {
			writeError(w, 404, fmt.Errorf("secret manager unavailable"))
			return
		}
		writeJSON(w, 200, values)
		return
	}
	var request struct {
		Key      string `json:"key"`
		Origin   string `json:"origin"`
		Value    string `json:"value"`
		Generate bool   `json:"generate"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	d.DisallowUnknownFields()
	if err := d.Decode(&request); err != nil {
		writeError(w, 400, fmt.Errorf("invalid secret request"))
		return
	}
	if err := d.Decode(new(any)); err != io.EOF {
		writeError(w, 400, fmt.Errorf("unexpected request content"))
		return
	}
	if (request.Generate && request.Value != "") || (!request.Generate && request.Value == "") {
		writeError(w, 400, fmt.Errorf("provide a value or request generation"))
		return
	}
	plain := []byte(request.Value)
	request.Value = ""
	defer clear(plain)
	value, created, err := s.Store.EnsureDesktopSecret(r.Context(), p, box, request.Key, request.Origin, plain)
	if err != nil {
		writeError(w, 400, fmt.Errorf("could not save secret; check the key, origin, box and encryption configuration"))
		return
	}
	writeJSON(w, 200, map[string]any{"secret": value, "created": created})
}

func (s *Server) deleteDesktopSecret(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	box, key := r.PathValue("id"), r.PathValue("key")
	resolved, err := s.Store.LogicalBox(r.Context(), p, box)
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	box = resolved.ID
	result, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM desktop_secrets WHERE account_id=$1 AND box_id=$2 AND secret_key=$3`, p.AccountID, box, key)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not remove secret"))
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		writeError(w, 404, fmt.Errorf("secret unavailable"))
		return
	}
	w.WriteHeader(204)
}

// Confirmation records the owner's observation, never an inference from filling
// a field. It changes metadata only, retaining the exact encrypted credential.
func (s *Server) confirmDesktopSecret(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	key := r.PathValue("key")
	if !loginProfileName.MatchString(key) {
		writeError(w, 400, fmt.Errorf("invalid secret reference"))
		return
	}
	resolved, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	result, err := s.Store.DB.ExecContext(r.Context(), `UPDATE desktop_secrets SET status='confirmed' WHERE account_id=$1 AND box_id=$2 AND secret_key=$3`, p.AccountID, resolved.ID, key)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not confirm secret"))
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		writeError(w, 404, fmt.Errorf("secret reference unavailable"))
		return
	}
	writeJSON(w, 200, map[string]string{"key": key, "status": "confirmed"})
}
