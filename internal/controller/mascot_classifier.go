package controller

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/0xikarus/vmbox-service/internal/mascotclass"
)

// MascotState contains the derived state and its observation time. The
// controller treats sampled transcript text as untrusted input and does not persist it.
type MascotState struct {
	Mood       string     `json:"mood"`
	Activity   string     `json:"activity"`
	ObservedAt *time.Time `json:"observedAt,omitempty"`
}

//go:embed mascot_model.bin
var mascotModelBytes []byte

var mascotModel = func() *mascotclass.Model {
	model, err := mascotclass.Load(mascotModelBytes)
	if err != nil {
		panic(err)
	}
	return model
}()

// classifyMascotText is source-independent: bounded text in, mascot state out.
func classifyMascotText(sample string) MascotState {
	label := mascotModel.Classify(sample)
	switch label {
	case "working":
		return MascotState{Mood: "idle", Activity: "working"}
	case "waiting":
		return MascotState{Mood: "waiting", Activity: "waiting"}
	default:
		return MascotState{Mood: label, Activity: "idle"}
	}
}

func (s *Server) mascotObservationHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var request struct {
		Session string `json:"session"`
		Text    string `json:"text"`
	}
	if err := decodeJSON(r, &request); err != nil || !validSessionName(request.Session) || len(request.Text) < 1 || len(request.Text) > 8192 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("valid session and bounded text required"))
		return
	}
	state := classifyMascotText(request.Text)
	phrase := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, activityPhrase(request.Text)))
	if runes := []rune(phrase); len(runes) > 48 {
		phrase = string(runes[:48])
	}
	result, err := s.Store.DB.ExecContext(r.Context(), `UPDATE box_tasks SET mascot_mood=$4,mascot_activity=$5,mascot_observed_at=now(),
		mascot_phrase=COALESCE(NULLIF($6,''),mascot_phrase),mascot_phrase_at=CASE WHEN $6<>'' THEN now() ELSE mascot_phrase_at END
		WHERE account_id=$1 AND logical_box_id=$2 AND session_name=$3 AND state='active' AND agent<>'shell'`, p.AccountID, r.PathValue("id"), request.Session, state.Mood, state.Activity, phrase)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("mascot state unavailable"))
		return
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		writeError(w, http.StatusConflict, fmt.Errorf("active agent chat session not found"))
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Store) boxMascotState(ctx context.Context, accountID, boxID string) (MascotState, bool, error) {
	var mood, activity sql.NullString
	var observed sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT mascot_mood,mascot_activity,mascot_observed_at FROM box_tasks
		WHERE account_id=$1 AND logical_box_id=$2 AND state='active' AND agent<>'shell'
		ORDER BY created_at DESC,id DESC LIMIT 1`, accountID, boxID).Scan(&mood, &activity, &observed)
	if errors.Is(err, sql.ErrNoRows) {
		return MascotState{}, false, nil
	}
	if err != nil {
		return MascotState{}, false, err
	}
	if !mood.Valid || !activity.Valid || !observed.Valid || time.Since(observed.Time) > 40*time.Second {
		return MascotState{}, false, nil
	}
	stamp := observed.Time.UTC()
	return MascotState{Mood: mood.String, Activity: activity.String, ObservedAt: &stamp}, true, nil
}
