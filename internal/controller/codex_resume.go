package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type codexResumeDecision struct {
	SavedAt time.Time `json:"savedAt"`
	Choice  string    `json:"choice"`
}

func (s *Server) codexResumeHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	box, err := s.Store.LogicalBox(ctx, p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	if box.State != v1.LogicalBoxRunning || box.DefaultAgent != "codex" {
		if r.Method == http.MethodGet {
			writeJSON(w, 200, map[string]any{"candidate": nil})
			return
		}
		writeError(w, 409, fmt.Errorf("box must be running Codex"))
		return
	}
	tasks, err := s.Store.ListBoxTasks(ctx, p, box.ID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("chat task unavailable"))
		return
	}
	task := reusableBoxTask(tasks, box.State, "codex", "")
	if task == nil || task.State != "active" {
		if r.Method == http.MethodGet {
			writeJSON(w, 200, map[string]any{"candidate": nil})
			return
		}
		writeError(w, 409, fmt.Errorf("Codex chat is not active"))
		return
	}
	a, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || a.Box.State != v1.LogicalBoxRunning || a.Slot.ServiceID == "" {
		writeError(w, 409, fmt.Errorf("box assignment unavailable"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, fmt.Errorf("worker unavailable"))
		return
	}
	// A running box does not pass through wake's runtime installation when the
	// controller is deployed. A previous scanner can validly return null, so
	// checking only for an unknown command would silently hide the offer.
	if len(s.WorkerRuntime) > 0 {
		digest := fmt.Sprintf("%x", sha256.Sum256(s.WorkerRuntime))
		matches, _ := installedWorkspaceRuntimeMatches(ctx, digest, func(ctx context.Context, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
			return prov.Exec(ctx, a.Slot.ServiceID, argv, opts)
		})
		if !matches {
			if err := stageWorkspaceRuntime(ctx, prov, a.Slot.ServiceID, s.WorkerRuntime); err != nil {
				writeError(w, 502, fmt.Errorf("could not update Codex workspace runtime"))
				return
			}
		}
	}
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "codex-resume-candidate", task.Session}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		writeError(w, 502, fmt.Errorf("saved Codex session lookup failed"))
		return
	}
	var candidate *boxruntime.CodexResumeCandidate
	if err := json.Unmarshal([]byte(result.Stdout), &candidate); err != nil {
		writeError(w, 502, fmt.Errorf("invalid saved Codex session response"))
		return
	}
	var raw []byte
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(metadata->'codexResumeDecision','null'::jsonb) FROM logical_boxes WHERE account_id=$1 AND id=$2`, p.AccountID, box.ID).Scan(&raw); err != nil {
		writeError(w, 500, fmt.Errorf("resume choice unavailable"))
		return
	}
	var decision codexResumeDecision
	_ = json.Unmarshal(raw, &decision)
	if candidate == nil || candidate.SavedAt.Equal(decision.SavedAt) {
		if r.Method == http.MethodGet {
			writeJSON(w, 200, map[string]any{"candidate": nil})
			return
		}
		writeError(w, 409, fmt.Errorf("no pending saved Codex session"))
		return
	}
	// A new owner message after the hibernation snapshot has already committed
	// to the fresh conversation. Do not replace it with older context.
	var sent bool
	err = s.Store.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM box_messages m JOIN box_tasks t ON t.id=m.task_id WHERE m.account_id=$1 AND t.logical_box_id=$2 AND m.direction IN ('user','box') AND m.created_at>$3)`, p.AccountID, box.ID, candidate.SavedAt).Scan(&sent)
	if err != nil {
		writeError(w, 500, fmt.Errorf("chat state unavailable"))
		return
	}
	if sent {
		if r.Method == http.MethodGet {
			writeJSON(w, 200, map[string]any{"candidate": nil})
			return
		}
		writeError(w, 409, fmt.Errorf("a new chat message was sent after wake; saved session can no longer replace it"))
		return
	}
	if r.Method == http.MethodGet {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"candidate": candidate})
		return
	}
	var request struct {
		Choice    string    `json:"choice"`
		SessionID string    `json:"sessionId"`
		SavedAt   time.Time `json:"savedAt"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	if request.Choice != "restore" && request.Choice != "fresh" {
		writeError(w, 400, fmt.Errorf("choice must be restore or fresh"))
		return
	}
	if request.SessionID != candidate.SessionID || !request.SavedAt.Equal(candidate.SavedAt) {
		writeError(w, 409, fmt.Errorf("saved session changed; refresh Chat"))
		return
	}
	if request.Choice == "restore" {
		result, err = prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "codex-resume-session", task.Session, candidate.SessionID, candidate.SavedAt.Format(time.RFC3339Nano)}, provider.ExecOptions{})
		if err != nil || result.ExitCode != 0 {
			writeError(w, 409, fmt.Errorf("Codex session could not be restored: %s", strings.TrimSpace(result.Stderr)))
			return
		}
	}
	encoded, _ := json.Marshal(codexResumeDecision{SavedAt: candidate.SavedAt, Choice: request.Choice})
	updated, err := s.Store.DB.ExecContext(ctx, `UPDATE logical_boxes SET metadata=jsonb_set(metadata,'{codexResumeDecision}',$3::jsonb),updated_at=now() WHERE account_id=$1 AND id=$2 AND state='running' AND slot_id=$4 AND assignment_generation=$5 AND fencing_token=$6`, p.AccountID, box.ID, string(encoded), a.Slot.ID, a.Box.AssignmentGeneration, a.FencingToken)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not save resume choice"))
		return
	}
	if count, _ := updated.RowsAffected(); count != 1 {
		writeError(w, 409, fmt.Errorf("box assignment changed"))
		return
	}
	writeJSON(w, 200, map[string]any{"choice": request.Choice})
}
