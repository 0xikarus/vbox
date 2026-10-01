package controller

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"
)

type boxActivity struct {
	BoxID          string     `json:"boxId"`
	Busy           *bool      `json:"busy"`
	BusySince      *time.Time `json:"busySince,omitempty"`
	Mood           string     `json:"mood,omitempty"`
	Activity       string     `json:"activity,omitempty"`
	Phrase         string     `json:"phrase,omitempty"`
	ObservedAt     *time.Time `json:"observedAt,omitempty"`
	LastObservedAt *time.Time `json:"lastObservedAt,omitempty"`
	LastMood       string     `json:"lastMood,omitempty"`
	LastActivity   string     `json:"lastActivity,omitempty"`
	LastPhrase     string     `json:"lastPhrase,omitempty"`
	LastPhraseAt   *time.Time `json:"lastPhraseAt,omitempty"`
}

// boxActivityHandler fetches the latest active task for each visible box in
// one account-scoped query. Inactive boxes cannot inherit stale task activity.
func (s *Server) boxActivityHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT b.id::text,t.agent_busy,t.agent_busy_updated_at,t.mascot_mood,t.mascot_activity,t.mascot_phrase,t.mascot_observed_at,t.mascot_phrase_at
		FROM logical_boxes b LEFT JOIN LATERAL (
			SELECT agent_busy,agent_busy_updated_at,mascot_mood,mascot_activity,mascot_phrase,mascot_observed_at,mascot_phrase_at
			FROM box_tasks WHERE account_id=b.account_id AND logical_box_id=b.id AND state='active' AND agent<>'shell' AND b.state='running'
			ORDER BY created_at DESC,id DESC LIMIT 1
		) t ON true
		WHERE b.account_id=$1 AND ($2='owner' OR b.owner_user_id=$3)
		ORDER BY b.name,b.id`, p.AccountID, p.Role, p.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("box activity unavailable"))
		return
	}
	defer rows.Close()
	result := make([]boxActivity, 0)
	now := time.Now()
	for rows.Next() {
		var value boxActivity
		var busy sql.NullBool
		var busySince, observed, phraseAt sql.NullTime
		var mood, activity, phrase sql.NullString
		if err := rows.Scan(&value.BoxID, &busy, &busySince, &mood, &activity, &phrase, &observed, &phraseAt); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("box activity unavailable"))
			return
		}
		if busy.Valid {
			value.Busy = &busy.Bool
		}
		if busySince.Valid {
			stamp := busySince.Time.UTC()
			value.BusySince = &stamp
		}
		if observed.Valid {
			age := now.Sub(observed.Time)
			stamp := observed.Time.UTC()
			value.LastObservedAt = &stamp
			value.LastMood = mood.String
			value.LastActivity = activity.String
			if age <= 40*time.Second {
				value.ObservedAt = &stamp
				value.Mood = mood.String
				value.Activity = activity.String
			}
		}
		if phraseAt.Valid && phrase.Valid && phrase.String != "" {
			stamp := phraseAt.Time.UTC()
			value.LastPhraseAt = &stamp
			value.LastPhrase = phrase.String
			// The list and chat show only the current observation's phrase. A
			// later unsure run retains LastPhrase for Details without replaying it.
			if observed.Valid && value.Busy != nil && *value.Busy && !phraseAt.Time.Before(observed.Time) && now.Sub(observed.Time) <= 10*time.Minute {
				value.Phrase = phrase.String
			}
		}
		result = append(result, value)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("box activity unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, result)
}
