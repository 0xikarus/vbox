package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// captchaSolverAPI is the owner toggle for automatic captcha solving. Responses
// never include the stored API key; only whether one is configured.
func (s *Server) getCaptchaSolver(w http.ResponseWriter, r *http.Request, p Principal) {
	setting, _, err := s.Store.CaptchaSolver(r.Context(), p.AccountID)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	writeJSON(w, 200, setting)
}

func (s *Server) putCaptchaSolver(w http.ResponseWriter, r *http.Request, p Principal) {
	var request struct {
		APIKey  string `json:"apiKey"`
		Enabled bool   `json:"enabled"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeError(w, 400, fmt.Errorf("apiKey and enabled are required"))
		return
	}
	setting, err := s.Store.PutCaptchaSolver(r.Context(), p, request.APIKey, request.Enabled)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, setting)
}

func (s *Server) deleteCaptchaSolver(w http.ResponseWriter, r *http.Request, p Principal) {
	if err := s.Store.DeleteCaptchaSolver(r.Context(), p); err != nil {
		writeError(w, 404, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
