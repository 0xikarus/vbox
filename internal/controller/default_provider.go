package controller

import (
	"fmt"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func (s *Server) defaultProviderHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == http.MethodPut {
		if p.Role != "owner" {
			writeError(w, 403, fmt.Errorf("owner role required"))
			return
		}
		var req v1.FleetConfig
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, 400, err)
			return
		}
		// Initial selection is explicit and cannot overwrite another default.
		result, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO controller_defaults(account_id,provider,provider_credential) SELECT account_id,provider,name FROM provider_credentials WHERE account_id=$1 AND provider=$2 AND name=$3 ON CONFLICT(account_id) DO UPDATE SET provider=excluded.provider WHERE controller_defaults.provider=excluded.provider AND controller_defaults.provider_credential=excluded.provider_credential`, p.AccountID, req.Provider, req.ProviderCredential)
		if err != nil {
			writeError(w, 409, fmt.Errorf("default selection failed"))
			return
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			writeError(w, 409, fmt.Errorf("alias missing or different default exists; explicit migration required"))
			return
		}
	}
	var value v1.FleetConfig
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT provider,provider_credential FROM controller_defaults WHERE account_id=$1`, p.AccountID).Scan(&value.Provider, &value.ProviderCredential)
	if err != nil {
		writeError(w, 409, fmt.Errorf("controller provider default not configured; use providers default PROVIDER NAME"))
		return
	}
	writeJSON(w, 200, value)
}
