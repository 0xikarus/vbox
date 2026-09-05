package controller

import (
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"net/http"
)

// Status uses stored observations only. It never resolves a provider or wakes
// compute; timestamps and an explicit cached marker prevent implying freshness.
func (s *Server) boxStatusHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	tasks, err := s.Store.ListBoxTasks(r.Context(), p, box.ID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT box_id::text,session_name,incarnation,revision::text,state,observed_at,partial FROM session_observations WHERE account_id=$1 AND box_id=$2 ORDER BY sequence DESC LIMIT 256`, p.AccountID, box.ID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer rows.Close()
	sessions := []v1.SessionUpdate{}
	for rows.Next() {
		var v v1.SessionUpdate
		if err = rows.Scan(&v.LogicalBoxID, &v.Session, &v.Incarnation, &v.Revision, &v.State, &v.ObservedAt, &v.Partial); err != nil {
			writeError(w, 500, err)
			return
		}
		sessions = append(sessions, v)
	}
	if err = rows.Err(); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, struct {
		v1.LogicalBox
		Tasks            []v1.BoxTask       `json:"tasks"`
		Sessions         []v1.SessionUpdate `json:"sessions"`
		ObservationState string             `json:"observationState"`
	}{box, tasks, sessions, "cached-or-unknown; run sessions/updates for a bounded live observation"})
}
