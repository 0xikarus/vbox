package controller

import (
	"context"
	"crypto/sha256"
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

const mascotQuietWindow = 90 * time.Second

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
	}, observationActivityPhrase(request.Text)))
	if runes := []rune(phrase); len(runes) > 48 {
		phrase = string(runes[:48])
	}
	// Keep only a digest of the bounded evidence. A repeated keepalive confirms
	// connectivity, but must not turn an old activity into fresh work.
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(request.Text)))
	var stored MascotState
	var observed time.Time
	err := s.Store.DB.QueryRowContext(r.Context(), `UPDATE box_tasks SET
		mascot_mood=CASE WHEN mascot_evidence_hash=$4 AND
			GREATEST(COALESCE(mascot_evidence_changed_at,now()),COALESCE(agent_busy_updated_at,'-infinity'::timestamptz))<=now()-($8::int * interval '1 second')
			THEN 'idle' ELSE $5 END,
		mascot_activity=CASE WHEN mascot_evidence_hash=$4 AND
			GREATEST(COALESCE(mascot_evidence_changed_at,now()),COALESCE(agent_busy_updated_at,'-infinity'::timestamptz))<=now()-($8::int * interval '1 second')
			THEN 'idle' ELSE $6 END,
		mascot_observed_at=now(),
		mascot_phrase=CASE WHEN mascot_evidence_hash IS DISTINCT FROM $4 AND $7<>'' THEN $7 ELSE mascot_phrase END,
		mascot_phrase_at=CASE WHEN mascot_evidence_hash IS DISTINCT FROM $4 AND $7<>'' THEN now() ELSE mascot_phrase_at END,
		mascot_evidence_changed_at=CASE WHEN mascot_evidence_hash IS DISTINCT FROM $4 THEN now() ELSE mascot_evidence_changed_at END,
		mascot_evidence_hash=$4
		WHERE account_id=$1 AND logical_box_id=$2 AND session_name=$3 AND state='active' AND agent<>'shell'
		RETURNING mascot_mood,mascot_activity,mascot_observed_at`, p.AccountID, r.PathValue("id"), request.Session, digest, state.Mood, state.Activity, phrase, int(mascotQuietWindow/time.Second)).
		Scan(&stored.Mood, &stored.Activity, &observed)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusConflict, fmt.Errorf("active agent chat session not found"))
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("mascot state unavailable"))
		return
	}
	stamp := observed.UTC()
	stored.ObservedAt = &stamp
	writeJSON(w, http.StatusOK, stored)
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
