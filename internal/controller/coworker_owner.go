package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func (s *Server) coworkerSettings(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == "PUT" {
		var request struct {
			Enabled *bool `json:"enabled"`
		}
		if decodeJSON(r, &request) != nil || request.Enabled == nil {
			writeError(w, 400, fmt.Errorf("explicit enabled boolean required"))
			return
		}
		if err := s.Store.EnableCoworkers(r.Context(), p, *request.Enabled); err != nil {
			writeError(w, 500, fmt.Errorf("could not update coworker gate"))
			return
		}
	}
	var enabled bool
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT enabled FROM coworker_settings WHERE account_id=$1),false)`, p.AccountID).Scan(&enabled); err != nil {
		writeError(w, 500, fmt.Errorf("could not read coworker gate"))
		return
	}
	writeJSON(w, 200, map[string]bool{"enabled": enabled})
}

func (s *Server) coworkerOwnerMessages(w http.ResponseWriter, r *http.Request, p Principal) {
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT e.sequence,COALESCE(s.name,'owner'),t.name,e.kind,e.data,e.created_at FROM coworker_events e LEFT JOIN logical_boxes s ON s.id=e.sender_box_id AND s.account_id=e.account_id JOIN logical_boxes t ON t.id=e.recipient_box_id AND t.account_id=e.account_id WHERE e.account_id=$1 ORDER BY e.sequence DESC LIMIT 100`, p.AccountID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not list coworker messages"))
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var sequence int64
		var sender, recipient, kind, created string
		var data json.RawMessage
		if err = rows.Scan(&sequence, &sender, &recipient, &kind, &data, &created); err != nil {
			writeError(w, 500, fmt.Errorf("could not read coworker messages"))
			return
		}
		out = append(out, map[string]any{"sequence": sequence, "sender": sender, "recipient": recipient, "kind": kind, "data": data, "createdAt": created})
	}
	if rows.Err() != nil {
		writeError(w, 500, fmt.Errorf("coworker message listing interrupted"))
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) coworkerOwnerList(w http.ResponseWriter, r *http.Request, p Principal) {
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT b.id::text,b.name,b.state,b.default_agent,c.enabled AND g.enabled FROM coworkers c JOIN coworker_settings g ON g.account_id=c.account_id JOIN logical_boxes b ON b.id=c.box_id AND b.account_id=c.account_id WHERE c.account_id=$1 ORDER BY b.name`, p.AccountID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not list coworkers"))
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, state, agent string
		var enabled bool
		if err = rows.Scan(&id, &name, &state, &agent, &enabled); err != nil {
			writeError(w, 500, fmt.Errorf("could not read coworkers"))
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "state": state, "agent": agent, "enabled": enabled})
	}
	if rows.Err() != nil {
		writeError(w, 500, fmt.Errorf("coworker listing interrupted"))
		return
	}
	writeJSON(w, 200, out)
}
