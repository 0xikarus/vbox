package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Server) startDesktop(w http.ResponseWriter, r *http.Request, p Principal) {
	s.desktopAction(w, r, p, "desktop-start")
}
func (s *Server) enableDesktop(w http.ResponseWriter, r *http.Request, p Principal) {
	s.desktopAction(w, r, p, "desktop-enable")
}
func (s *Server) desktopStatus(w http.ResponseWriter, r *http.Request, p Principal) {
	s.desktopAction(w, r, p, "desktop-status")
}
func (s *Server) desktopAction(w http.ResponseWriter, r *http.Request, p Principal, command string) {
	duration := 25 * time.Second
	if command == "desktop-enable" {
		duration = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), duration)
	defer cancel()
	box, err := s.Store.LogicalBox(ctx, p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	a, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || a.Box.State != "running" {
		writeError(w, 409, fmt.Errorf("resume the box before starting its desktop"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", command, nativeFence(a)}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		writeError(w, 409, fmt.Errorf("desktop startup failed; worker needs desktop components and matching runtime; inspect vmbox-desktop session"))
		return
	}
	current, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || nativeFence(current) != nativeFence(a) {
		writeError(w, 409, fmt.Errorf("assignment changed"))
		return
	}
	if command == "desktop-status" {
		var status struct { Enabled bool `json:"enabled"` }
		if err := json.Unmarshal([]byte(result.Stdout), &status); err != nil {
			writeError(w, 502, fmt.Errorf("invalid desktop status from worker")); return
		}
		writeJSON(w, 200, status)
		return
	}
	w.WriteHeader(204)
}
