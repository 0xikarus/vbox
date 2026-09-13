package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Server) desktopControl(w http.ResponseWriter, r *http.Request, p Principal) {
	var request struct {
		Action string `json:"action"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || (request.Action != "pause" && request.Action != "resume") {
		writeError(w, 400, fmt.Errorf("action must be pause or resume"))
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, 400, fmt.Errorf("unexpected request content"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	box, err := s.Store.LogicalBox(ctx, p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	a, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || a.Box.State != "running" {
		writeError(w, 409, fmt.Errorf("desktop is offline"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, fmt.Errorf("worker unavailable"))
		return
	}
	data, _ := json.Marshal(request)
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "desktop-input", nativeFence(a)}, provider.ExecOptions{Stdin: bytes.NewReader(data), Stdout: io.Discard, Stderr: io.Discard})
	if err != nil || result.ExitCode != 0 {
		writeError(w, 409, fmt.Errorf("could not change desktop control; reconnect and retry"))
		return
	}
	current, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || current.Box.State != "running" || nativeFence(current) != nativeFence(a) {
		writeError(w, 409, fmt.Errorf("assignment changed"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]bool{"paused": request.Action == "pause"})
}
