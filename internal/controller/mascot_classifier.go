package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// MascotState is deliberately small: no model, transcript, or confidence
// history is kept. Terminal text is untrusted evidence, never an instruction.
type MascotState struct {
	Mood     string `json:"mood"`
	Activity string `json:"activity"`
}

var mascotPatterns = struct {
	failure, success, laugh, waiting, work *regexp.Regexp
}{
	failure: regexp.MustCompile(`(?i)(?:^|\b)(?:error:|failed\b|failure\b|panic:|exception\b|fatal:|traceback\b|timed out\b|permission denied\b|cannot\b|unable to\b)`),
	success: regexp.MustCompile(`(?i)(?:\btests? pass(?:ed)?\b|\bchecks? pass(?:ed)?\b|\bsuccess(?:ful(?:ly)?)?\b|\bcompleted\b|\bfinished\b|\bfixed\b|\bresolved\b|\bshipped\b|\bmerged\b|\blooks good\b)`),
	laugh:   regexp.MustCompile(`(?i)(?:\bhaha(?:ha)*\b|\blol\b|\blmao\b|😂|🤣|\bthat's funny\b)`),
	waiting: regexp.MustCompile(`(?i)(?:\bwaiting for (?:you|user|input|approval|confirmation)\b|\bplease (?:confirm|choose|approve|select)\b|\bneed your (?:input|approval|decision)\b|\bwhich (?:option|one)\b|\b(?:would you|could you|do you want|should I|shall I|can you)[^?\n]{0,120}\?)`),
	work:    regexp.MustCompile(`(?i)(?:\bthinking\b|\bworking\b|\brunning\b|\bbuilding\b|\bcompiling\b|\bsearching\b|\bimplementing\b|\btesting\b|\bwriting\b|\banalyzing\b|\bprocessing\b|\binspecting\b|\breviewing\b|\bdebugging\b|\binvestigating\b|\bchecking\b|\bplanning\b)`),
}

// classifyMascotText weights the newest meaningful lines. Explicit result
// phrases beat older errors, so a completed fix does not leave an angry mascot.
// This is a bounded lexical classifier: it uses negligible memory and has no
// external service or model download.
func classifyMascotText(sample string) MascotState {
	state := MascotState{Mood: "idle", Activity: "idle"}
	lines := strings.Split(sample, "\n")
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}
	var failure, success, laugh, waiting, work int
	seen := 0
	activeTool := false
	insideFence := false
	for i := len(lines) - 1; i >= 0 && seen < 24; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || len(line) > 1000 {
			continue
		}
		// Native excerpts label roles. A user's request to fix an error is
		// context, not evidence that the agent has failed.
		if strings.HasPrefix(line, "user: ") {
			continue
		}
		if seen == 0 && line == "tool: Running tool" {
			activeTool = true
		}
		line = strings.TrimPrefix(strings.TrimPrefix(line, "assistant: "), "tool: ")
		// Code and command text often mention errors as examples. Let status and
		// prose lines supply the evidence instead.
		if strings.HasPrefix(line, "```") {
			insideFence = !insideFence
			continue
		}
		if insideFence || strings.HasPrefix(line, "$ ") || strings.HasPrefix(line, "> ") {
			continue
		}
		weight := 1
		if seen < 6 {
			weight = 3
		} else if seen < 12 {
			weight = 2
		}
		if mascotPatterns.failure.MatchString(line) {
			failure += weight
		}
		if mascotPatterns.success.MatchString(line) {
			success += weight
		}
		if mascotPatterns.laugh.MatchString(line) {
			laugh += weight
		}
		if mascotPatterns.waiting.MatchString(line) {
			waiting += weight
		}
		if mascotPatterns.work.MatchString(line) {
			work += weight
		}
		seen++
	}
	switch {
	case waiting > 0 && waiting >= failure && waiting >= success:
		state.Mood, state.Activity = "waiting", "waiting"
	case laugh > 0 && laugh >= failure && laugh >= success:
		state.Mood = "laughing"
	case failure > success && failure > 0:
		state.Mood = "angry"
	case success > 0:
		state.Mood = "happy"
	}
	if activeTool {
		return MascotState{Mood: "idle", Activity: "working"}
	}
	if state.Activity == "idle" && work > 0 && work > success && work > failure {
		state.Activity = "working"
	}
	return state
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
