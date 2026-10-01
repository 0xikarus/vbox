package controller

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Read markers are separate from the sidebar layout so a stale layout save
// cannot move a marker backwards. Each key advances atomically in the database.
func validChatReadKey(key string) bool {
	if len(key) > 160 {
		return false
	}
	validID := func(id string) bool {
		if id == "" || len(id) > 80 {
			return false
		}
		for _, char := range id {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.') {
				return false
			}
		}
		return true
	}
	if strings.HasPrefix(key, "box:") {
		return validID(strings.TrimPrefix(key, "box:"))
	}
	if strings.HasPrefix(key, "pair:") {
		parts := strings.Split(strings.TrimPrefix(key, "pair:"), "/")
		return len(parts) == 2 && validID(parts[0]) && validID(parts[1])
	}
	return false
}

func (s *Server) chatReadMarkersHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPut {
		var incoming map[string]string
		if err := decodeJSON(r, &incoming); err != nil || len(incoming) > 5000 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid chat read markers"))
			return
		}
		times := make(map[string]time.Time, len(incoming))
		keys := make([]string, 0, len(incoming))
		for key, value := range incoming {
			at, err := time.Parse(time.RFC3339Nano, value)
			if !validChatReadKey(key) || err != nil || at.IsZero() || at.After(time.Now().Add(time.Minute)) {
				writeError(w, http.StatusBadRequest, fmt.Errorf("invalid chat read marker"))
				return
			}
			times[key] = at.UTC()
			keys = append(keys, key)
		}
		sort.Strings(keys)
		tx, err := s.Store.DB.BeginTx(r.Context(), nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		defer tx.Rollback()
		for _, key := range keys {
			at := times[key]
			_, err = tx.ExecContext(r.Context(), `INSERT INTO chat_read_markers(account_id,chat_key,seen_at) VALUES($1,$2,$3)
				ON CONFLICT(account_id,chat_key) DO UPDATE SET seen_at=GREATEST(chat_read_markers.seen_at,excluded.seen_at)`, p.AccountID, key, at)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
		}
		if err = tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT chat_key,seen_at FROM chat_read_markers WHERE account_id=$1`, p.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	markers := map[string]string{}
	for rows.Next() {
		var key string
		var at time.Time
		if err = rows.Scan(&key, &at); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		markers[key] = at.UTC().Format(time.RFC3339Nano)
	}
	if err = rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, markers)
}
