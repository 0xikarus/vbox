package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/coworker"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Server) spawnCoworker(w http.ResponseWriter, r *http.Request, p Principal) {
	var req struct {
		Agent                   string `json:"agent"`
		Prompt                  string `json:"prompt"`
		Confirm                 bool   `json:"confirm"`
		AllowDevelopmentChannel bool   `json:"allowDevelopmentChannel"`
	}
	if decodeJSON(r, &req) != nil || !req.Confirm || (req.Agent != "codex" && req.Agent != "claude") || strings.TrimSpace(req.Prompt) == "" || len(req.Prompt) > 65536 {
		writeError(w, 400, fmt.Errorf("explicit confirmation, claude/codex agent, and prompt up to 64 KiB required"))
		return
	}
	if req.Agent == "claude" && !req.AllowDevelopmentChannel {
		writeError(w, 400, fmt.Errorf("Claude requires explicit development-channel consent; attach to accept its startup dialog"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	box, err := s.Store.LogicalBox(ctx, p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("logical box not found"))
		return
	}
	if !strings.HasPrefix(box.Name, "coworker-") || box.State != v1.LogicalBoxRunning {
		writeError(w, 409, fmt.Errorf("allocate an explicitly named coworker- box first"))
		return
	}
	if s.PublicURL == "" || s.Store.Envelope == nil {
		writeError(w, 409, fmt.Errorf("controller public URL and encryption required"))
		return
	}
	var enrolled bool
	if err = s.Store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coworkers WHERE account_id=$1 AND box_id=$2)`, p.AccountID, box.ID).Scan(&enrolled); err != nil {
		writeError(w, 500, fmt.Errorf("could not check coworker enrollment"))
		return
	}
	if !enrolled {
		if err = s.Store.EnrollCoworker(ctx, p, box.ID); err != nil {
			writeError(w, 409, err)
			return
		}
	}
	var sealed string
	if err = s.Store.DB.QueryRowContext(ctx, `SELECT c.encrypted_token FROM coworkers c JOIN coworker_settings g ON g.account_id=c.account_id WHERE c.account_id=$1 AND c.box_id=$2 AND c.enabled AND g.enabled`, p.AccountID, box.ID).Scan(&sealed); err != nil {
		writeError(w, 409, fmt.Errorf("coworker gate disabled"))
		return
	}
	token, err := s.Store.Envelope.Open(p.AccountID+":coworker:"+box.ID, sealed)
	if err != nil {
		writeError(w, 500, fmt.Errorf("coworker credential unavailable"))
		return
	}
	defer clear(token)
	a, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil {
		writeError(w, 409, fmt.Errorf("box assignment unavailable"))
		return
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not lock assignment"))
		return
	}
	defer tx.Rollback()
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT assignment_generation FROM logical_boxes WHERE id=$1 AND account_id=$2 AND state='running' AND fencing_token=$3 FOR UPDATE`, box.ID, p.AccountID, a.FencingToken).Scan(&generation); err != nil || generation != a.Box.AssignmentGeneration {
		writeError(w, 409, fmt.Errorf("box assignment changed"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, fmt.Errorf("provider unavailable"))
		return
	}
	if err := verifyCredentialVolume(ctx, prov, a.Slot.ServiceID, a.Box.VolumeID); err != nil {
		writeError(w, 409, err)
		return
	}
	if err = stageWorkspaceRuntime(ctx, prov, a.Slot.ServiceID, s.WorkerRuntime); err != nil {
		writeError(w, 502, fmt.Errorf("could not stage coworker runtime"))
		return
	}
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "native-sessions", nativeFence(a)}, provider.ExecOptions{})
	var inventory v1.SessionInventory
	if err != nil || result.ExitCode != 0 || json.Unmarshal([]byte(result.Stdout), &inventory) != nil {
		writeError(w, 502, fmt.Errorf("could not check existing sessions"))
		return
	}
	for _, session := range inventory.Sessions {
		if session.Name == "coworker-primary" {
			writeError(w, 409, fmt.Errorf("coworker-primary already exists; attach instead of spawning again"))
			return
		}
	}
	configData, _ := json.Marshal(coworker.LaunchConfig{URL: s.PublicURL, Token: string(token), Agent: req.Agent, Prompt: req.Prompt, AllowDevelopmentChannel: req.AllowDevelopmentChannel})
	defer clear(configData)
	payload, _ := json.Marshal(boxruntime.SyncRequest{Files: []boxruntime.SyncFile{{Path: coworker.ConfigPath, Mode: "0600", Data: configData}}})
	defer clear(payload)
	result, err = prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "sync-files"}, provider.ExecOptions{Stdin: bytes.NewReader(payload)})
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != fmt.Sprintf("%x", sha256.Sum256(payload)) {
		writeError(w, 502, fmt.Errorf("coworker configuration transfer unconfirmed"))
		return
	}
	result, err = prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "coworker-start", nativeFence(a)}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		writeError(w, 502, fmt.Errorf("coworker startup unconfirmed; inspect sessions before retrying"))
		return
	}
	if _, err = tx.ExecContext(ctx, `UPDATE logical_boxes SET metadata=jsonb_set(metadata,'{primarySession}','"coworker-primary"'::jsonb),updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, box.ID); err != nil {
		writeError(w, 500, fmt.Errorf("coworker started but primary session could not be saved"))
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 409, fmt.Errorf("coworker started but assignment commit unconfirmed"))
		return
	}
	writeJSON(w, 201, map[string]string{"session": "coworker-primary", "state": "started", "agent": req.Agent})
}
