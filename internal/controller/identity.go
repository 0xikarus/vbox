package controller

import (
	"crypto/sha256"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/secrets"
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

// Rotate only the authenticated token, preserving the user's identity and grants.
func (s *Server) rotateToken(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var req struct {
		Hash []byte `json:"hash"`
	}
	if decodeJSON(r, &req) != nil || len(req.Hash) != sha256.Size {
		writeError(w, 400, fmt.Errorf("provide a SHA-256 token hash"))
		return
	}
	token, ok := authorizationValue(r.Header.Get("Authorization"), "Bearer")
	if !ok {
		writeError(w, 401, fmt.Errorf("missing bearer token"))
		return
	}
	old := secrets.TokenHash(token)
	result, err := s.Store.DB.ExecContext(r.Context(), `UPDATE access_tokens SET token_hash=$1 WHERE account_id=$2 AND user_id=$3 AND token_hash=$4 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now())`, req.Hash, p.AccountID, p.UserID, old)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not rotate token"))
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		writeError(w, 409, fmt.Errorf("token changed; verify your login"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
