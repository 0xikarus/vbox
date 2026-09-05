package controller

import (
	"fmt"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// Locations reflect the managed fleet, not a hardcoded list in each CLI.
func (s *Server) locationsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	name, alias, err := fleetTarget(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT region, count(*) FILTER (WHERE state='free' AND health='healthy') FROM compute_slots WHERE account_id=$1 AND provider=$2 AND provider_credential=$3 AND region IS NOT NULL AND region<>'' GROUP BY region ORDER BY region`, p.AccountID, name, alias)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not list fleet locations"))
		return
	}
	defer rows.Close()
	values := []v1.LocationPreset{}
	for rows.Next() {
		var value v1.LocationPreset
		if err := rows.Scan(&value.ID, &value.AvailableSlots); err != nil {
			writeError(w, 500, fmt.Errorf("could not read fleet locations"))
			return
		}
		values = append(values, value)
	}
	if rows.Err() != nil {
		writeError(w, 500, fmt.Errorf("could not read fleet locations"))
		return
	}
	writeJSON(w, 200, values)
}
