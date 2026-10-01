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

	"github.com/0xikarus/vmbox-service/internal/mascotclass"
)

// MascotState contains only the derived state. The controller treats sampled
// transcript text as untrusted input and does not persist it.
type MascotState struct {
	Mood     string `json:"mood"`
	Activity string `json:"activity"`
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

// The box sender labels native message roles. Filter structural transcript
// context before passing text to the generic classifier.
func mascotTranscriptEvidence(sample string) string {
	lines := strings.Split(sample, "\n")
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}
	retained := make([]string, 0, len(lines))
	insideFence := false
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		// User requests provide context, not the agent's current state.
		if strings.HasPrefix(line, "user: ") {
			continue
		}
		line = strings.TrimPrefix(strings.TrimPrefix(line, "assistant: "), "tool: ")
		// Code examples and echoed commands are not status reports.
		if strings.HasPrefix(line, "```") {
			insideFence = !insideFence
			continue
		}
		if insideFence || strings.HasPrefix(line, "$ ") || strings.HasPrefix(line, "> ") {
			continue
		}
		retained = append(retained, line)
	}
	for left, right := 0, len(retained)-1; left < right; left, right = left+1, right-1 {
		retained[left], retained[right] = retained[right], retained[left]
	}
	return strings.Join(retained, "\n")
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
	state := classifyMascotText(mascotTranscriptEvidence(request.Text))
	result, err := s.Store.DB.ExecContext(r.Context(), `UPDATE box_tasks SET mascot_mood=$4,mascot_activity=$5,mascot_observed_at=now()
		WHERE account_id=$1 AND logical_box_id=$2 AND session_name=$3 AND state='active' AND agent<>'shell'`, p.AccountID, r.PathValue("id"), request.Session, state.Mood, state.Activity)
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
	return MascotState{Mood: mood.String, Activity: activity.String}, true, nil
}
