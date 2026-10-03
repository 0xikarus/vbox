package controller

import (
	"fmt"
	"net/http"
)

// agentBoxControlHandler is the box-authenticated remote desktop action route.
func (s *Server) agentBoxControlHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	writeError(w, http.StatusNotImplemented, fmt.Errorf("remote desktop control is unavailable"))
}
