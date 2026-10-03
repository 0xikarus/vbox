package controller

import (
	"database/sql"
	"errors"
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
		if !errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("could not read controller provider default; retry later"))
			return
		}
		// A single configured provider is unambiguous even before an explicit
		// default has been saved. Resolve it for reads without changing settings.
		rows, queryErr := s.Store.DB.QueryContext(r.Context(), `SELECT provider,name FROM provider_credentials WHERE account_id=$1 AND deleting=false ORDER BY provider,name LIMIT 2`, p.AccountID)
		if queryErr != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("could not read providers; retry later"))
			return
		}
		defer rows.Close()
		if rows.Next() {
			if queryErr = rows.Scan(&value.Provider, &value.ProviderCredential); queryErr != nil {
				writeError(w, http.StatusInternalServerError, fmt.Errorf("could not read providers; retry later"))
				return
			}
			if !rows.Next() && rows.Err() == nil {
				writeJSON(w, 200, map[string]any{"provider": value.Provider, "providerCredential": value.ProviderCredential, "inferred": true})
				return
			}
		}
		if rows.Err() != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("could not read providers; retry later"))
			return
		}
		writeError(w, http.StatusConflict, fmt.Errorf("Choose a default provider in Providers"))
		return
	}
	writeJSON(w, 200, value)
}
