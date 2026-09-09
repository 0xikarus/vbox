package controller

import "net/http"

// Return the ordinary authorized box list minus disposable runs and boxes with
// unfinished one-shot processes. Do not infer purpose from user-chosen names or
// the paginated Run once history. This endpoint never allocates compute.
func (s *Server) gridBoxesHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	boxes, err := s.Store.ListLogicalBoxes(r.Context(), p, "", "")
	if err != nil {
		writeError(w, 500, err)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT box_id::text FROM run_once_requests WHERE account_id=$1 AND box_id IS NOT NULL UNION SELECT logical_box_id::text FROM process_tasks WHERE account_id=$1 AND result->>'finishedAt' IS NULL`, p.AccountID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer rows.Close()
	excluded := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			writeError(w, 500, err)
			return
		}
		excluded[id] = true
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, err)
		return
	}
	visible := boxes[:0]
	for _, box := range boxes {
		if !excluded[box.ID] {
			visible = append(visible, box)
		}
	}
	writeJSON(w, 200, visible)
}
