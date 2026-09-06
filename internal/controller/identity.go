package controller

import (
	"fmt"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func (s *Server) whoami(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	identity := v1.Identity{AccountID: p.AccountID, UserID: p.UserID, Subject: p.Subject, Role: p.Role}
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT name FROM accounts WHERE id=$1`, p.AccountID).Scan(&identity.AccountName); err != nil {
		writeError(w, 500, fmt.Errorf("could not read current account"))
		return
	}
	writeJSON(w, 200, identity)
}
