package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Server) logicalBoxLogs(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	tail := 200
	if value := strings.TrimSpace(r.URL.Query().Get("tail")); value != "" {
		tail, err = strconv.Atoi(value)
		if err != nil || tail < 1 || tail > 2000 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("tail must be between 1 and 2000 lines"))
			return
		}
	}
	assignment, err := s.Store.assignment(r.Context(), p.AccountID, box.ID)
	if err != nil || assignment.Slot.ServiceID == "" {
		writeError(w, http.StatusConflict, fmt.Errorf("box has no assigned worker"))
		return
	}
	prov, err := s.provider(r.Context(), p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	err = prov.Logs(ctx, assignment.Slot.ServiceID, provider.LogOptions{Tail: tail}, &output)
	if err != nil && output.Len() == 0 {
		writeError(w, http.StatusBadGateway, fmt.Errorf("worker logs unavailable: %w", err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err != nil {
		w.Header().Set("X-Vmbox-Logs-Partial", "true")
	}
	_, _ = w.Write(output.Bytes())
}
