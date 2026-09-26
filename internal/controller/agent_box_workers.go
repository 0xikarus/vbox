package controller

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
)

// An available worker is a healthy, unassigned compute slot in the creator's
// provider pool. A shared physical worker may offer several distinct slots.
type agentBoxWorker struct {
	SlotID      string `json:"slotId"`
	ServiceName string `json:"serviceName,omitempty"`
	Ordinal     int    `json:"ordinal"`
	Region      string `json:"region,omitempty"`
}

func (s *Server) agentBoxWorkersHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, agentBoxID(p))
	if err != nil {
		writeError(w, 500, fmt.Errorf("worker options unavailable"))
		return
	}
	if err := requireCapability(capabilities.CreateAgentBox.Enabled, "create_agent_box"); err != nil {
		writeError(w, 403, err)
		return
	}
	var providerName, credential string
	err = s.Store.DB.QueryRowContext(r.Context(), `SELECT provider,provider_credential FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state='running'`, p.AccountID, agentBoxID(p)).Scan(&providerName, &credential)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 409, fmt.Errorf("creator box is unavailable"))
		return
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("worker options unavailable"))
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT s.id::text,COALESCE(s.service_name,s.service_id,''),s.ordinal,COALESCE(s.region,'') FROM compute_slots s WHERE s.account_id=$1 AND s.provider=$2 AND s.provider_credential=$3 AND s.state='free' AND s.health='healthy' AND NOT EXISTS (SELECT 1 FROM logical_boxes assigned WHERE assigned.slot_id=s.id) ORDER BY s.ordinal,s.id`, p.AccountID, providerName, credential)
	if err != nil {
		writeError(w, 500, fmt.Errorf("worker options unavailable"))
		return
	}
	defer rows.Close()
	workers := []agentBoxWorker{}
	for rows.Next() {
		var worker agentBoxWorker
		if err := rows.Scan(&worker.SlotID, &worker.ServiceName, &worker.Ordinal, &worker.Region); err != nil {
			writeError(w, 500, fmt.Errorf("worker options unavailable"))
			return
		}
		workers = append(workers, worker)
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, fmt.Errorf("worker options unavailable"))
		return
	}
	writeJSON(w, 200, workers)
}
