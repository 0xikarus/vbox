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
	Status         string     `json:"status,omitempty"`
	StatusSource   string     `json:"statusSource,omitempty"`
	StatusAt       *time.Time `json:"statusAt,omitempty"`
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
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT b.id::text,t.agent_busy,t.agent_busy_updated_at,t.mascot_mood,t.mascot_activity,t.mascot_phrase,t.mascot_observed_at,t.mascot_phrase_at,t.mascot_evidence_changed_at
		FROM logical_boxes b LEFT JOIN LATERAL (
			SELECT agent_busy,agent_busy_updated_at,mascot_mood,mascot_activity,mascot_phrase,mascot_observed_at,mascot_phrase_at,mascot_evidence_changed_at
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
		var busySince, observed, phraseAt, evidenceAt sql.NullTime
		var mood, activity, phrase sql.NullString
		if err := rows.Scan(&value.BoxID, &busy, &busySince, &mood, &activity, &phrase, &observed, &phraseAt, &evidenceAt); err != nil {
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
		}
		value.Status, value.StatusSource, value.StatusAt = observedActivityStatus(now, busy, busySince, observed, evidenceAt, phraseAt, phrase.String, activity.String)
		if value.ObservedAt != nil && (value.StatusSource == "specific" || value.StatusSource == "fallback" || value.StatusSource == "quiet") {
			value.Phrase = value.Status
		}
		result = append(result, value)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("box activity unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// The model may abstain on a specific verb phrase. Busy still has a truthful
// generic status; repeated evidence becomes idle after three keepalive periods.
func observedActivityStatus(now time.Time, busy sql.NullBool, busySince, observed, evidenceAt, phraseAt sql.NullTime, phrase, activity string) (string, string, *time.Time) {
	stamp := func(at time.Time) *time.Time { value := at.UTC(); return &value }
	if !busy.Valid {
		return "", "", nil
	}
	if !busy.Bool {
		return "Idle", "not-busy", nil
	}
	if !observed.Valid {
		if busySince.Valid {
			return "Working", "busy", stamp(busySince.Time)
		}
		return "Working", "busy", nil
	}
	if now.Sub(observed.Time) > 40*time.Second {
		return "Unknown", "stale", stamp(observed.Time)
	}
	changedAt := observed.Time
	if evidenceAt.Valid {
		changedAt = evidenceAt.Time
	}
	if busySince.Valid && busySince.Time.After(changedAt) {
		changedAt = busySince.Time
	}
	if now.Sub(changedAt) >= mascotQuietWindow {
		return "Idle", "quiet", stamp(changedAt.Add(mascotQuietWindow))
	}
	if activity == "waiting" {
		return "Waiting", "classifier", stamp(observed.Time)
	}
	if phraseAt.Valid && phrase != "" && !phraseAt.Time.Before(changedAt) {
		return phrase, "specific", stamp(phraseAt.Time)
	}
	return "Working", "fallback", stamp(observed.Time)
}
