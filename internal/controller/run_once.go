package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
)

const archiveRunOnceSQL = `UPDATE run_once_requests q SET result=t.result,state='finished' FROM process_tasks t WHERE q.account_id=$1 AND q.box_id=$2 AND t.account_id=q.account_id AND t.id=q.id AND t.logical_box_id=q.box_id AND t.result->>'finishedAt' IS NOT NULL`

type runOnceRequest struct {
	SetupScript        string               `json:"setupScript,omitempty"`
	Tools              []string             `json:"tools,omitempty"`
	Images             []runOnceImageRef    `json:"images,omitempty"`
	Agent              string               `json:"agent"`
	Prompt             string               `json:"prompt"`
	Model              string               `json:"model,omitempty"`
	Args               []string             `json:"args,omitempty"`
	Provider           string               `json:"provider"`
	ProviderCredential string               `json:"providerCredential"`
	LoginProfiles      []v1.LoginProfileRef `json:"loginProfiles"`
}
type runOnceRecord struct {
	ID         string          `json:"id"`
	Request    runOnceRequest  `json:"request"`
	State      string          `json:"state"`
	BoxID      string          `json:"boxId,omitempty"`
	BoxDeleted bool            `json:"boxDeleted"`
	Failure    string          `json:"failure,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
	Task       *v1.ProcessTask `json:"task,omitempty"`
}

func (s *Server) boxRunOnce(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	var id string
	err = s.Store.DB.QueryRowContext(r.Context(), `SELECT id::text FROM run_once_requests WHERE account_id=$1 AND box_id=$2`, p.AccountID, box.ID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 200, nil)
		return
	}
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"id": id})
}

func (s *Server) validateRunOnce(ctx context.Context, p Principal, req runOnceRequest) error {
	if err := v1.ValidateSetupScript(req.SetupScript); err != nil {
		return err
	}
	if err := v1.ValidateTools(req.Tools); err != nil {
		return err
	}
	if _, err := s.runOnceImagePrompt(ctx, p.AccountID, req, false); err != nil {
		return err
	}
	if err := v1.ValidateProcessOptions(req.Agent, req.Model, req.Args); err != nil {
		return err
	}
	if (req.Agent != "claude" && req.Agent != "codex" && req.Agent != "opencode" && req.Agent != "shell") || strings.TrimSpace(req.Prompt) == "" || len(req.Prompt) > 100000 || req.Provider == "" || req.ProviderCredential == "" {
		return fmt.Errorf("select a provider, agent and a prompt or shell command (up to 100000 bytes)")
	}
	seen := map[string]bool{}
	for _, ref := range req.LoginProfiles {
		if seen[ref.Application] {
			return fmt.Errorf("select only one profile per application")
		}
		seen[ref.Application] = true
		profile, err := s.Store.LoadLoginProfile(ctx, p, ref.Application, ref.Name)
		if err != nil {
			return fmt.Errorf("selected %s profile is unavailable; upload it with vmbox profiles upload", ref.Application)
		}
		err = loginprofile.Validate(ref.Application, profile.Files, time.Now())
		for _, data := range profile.Files {
			clear(data)
		}
		if err != nil {
			return fmt.Errorf("%s/%s: %w", ref.Application, ref.Name, err)
		}
	}
	if req.Agent != "shell" && !seen[req.Agent] {
		return fmt.Errorf("select a saved %s login; upload local logins with vmbox profiles upload", req.Agent)
	}
	return nil
}

func (s *Server) createRunOnce(w http.ResponseWriter, r *http.Request, p Principal) {
	var req runOnceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" || len(key) > 256 {
		writeError(w, 400, fmt.Errorf("Idempotency-Key required (up to 256 bytes)"))
		return
	}
	data, _ := json.Marshal(req)
	// Exact retries return the accepted record even if credentials later expire.
	var id string
	var same bool
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT id::text,request=$3::jsonb FROM run_once_requests WHERE account_id=$1 AND request_key=$2`, p.AccountID, key, data).Scan(&id, &same)
	if err == nil {
		if !same {
			writeError(w, 409, fmt.Errorf("key belongs to a different request"))
			return
		}
		r.SetPathValue("id", id)
		s.listRunOnce(w, r, p)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 500, err)
		return
	}
	if err = s.validateRunOnce(r.Context(), p, req); err != nil {
		writeError(w, 400, err)
		return
	}
	id = uuid()
	err = s.Store.DB.QueryRowContext(r.Context(), `INSERT INTO run_once_requests(id,account_id,user_id,request_key,request) VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_id,request_key) DO UPDATE SET request_key=EXCLUDED.request_key RETURNING id::text,request=$5::jsonb`, id, p.AccountID, p.UserID, key, data).Scan(&id, &same)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if !same {
		writeError(w, 409, fmt.Errorf("key belongs to a different request"))
		return
	}
	r.SetPathValue("id", id)
	s.listRunOnce(w, r, p)
}

func (s *Server) listRunOnce(w http.ResponseWriter, r *http.Request, p Principal) {
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT q.id::text,q.request,q.state,COALESCE(q.box_id::text,''),q.failure,q.created_at,COALESCE(q.result,t.result),q.box_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM logical_boxes b WHERE b.id=q.box_id AND b.account_id=q.account_id) FROM run_once_requests q LEFT JOIN process_tasks t ON t.id=q.id AND t.account_id=q.account_id WHERE q.account_id=$1 AND ($2='' OR q.id::text=$2) ORDER BY q.created_at DESC LIMIT 100`, p.AccountID, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer rows.Close()
	values := []runOnceRecord{}
	for rows.Next() {
		var v runOnceRecord
		var req, task []byte
		if err = rows.Scan(&v.ID, &req, &v.State, &v.BoxID, &v.Failure, &v.CreatedAt, &task, &v.BoxDeleted); err != nil {
			writeError(w, 500, err)
			return
		}
		if err = json.Unmarshal(req, &v.Request); err != nil {
			writeError(w, 500, err)
			return
		}
		if len(task) > 0 {
			if err = json.Unmarshal(task, &v.Task); err != nil {
				writeError(w, 500, err)
				return
			}
			if r.PathValue("id") == "" {
				v.Task.Output = ""
			}
		}
		values = append(values, v)
	}
	if err = rows.Err(); err != nil {
		writeError(w, 500, err)
		return
	}
	if r.PathValue("id") != "" {
		if len(values) != 1 {
			writeError(w, 404, fmt.Errorf("run not found"))
			return
		}
		writeJSON(w, 200, values[0])
		return
	}
	writeJSON(w, 200, values)
}

func (s *Server) cancelRunOnce(w http.ResponseWriter, r *http.Request, p Principal) {
	result, err := s.Store.DB.ExecContext(r.Context(), `UPDATE run_once_requests q SET state='cancelled' WHERE account_id=$1 AND id::text=$2 AND state='queued' AND box_id IS NULL AND NOT EXISTS(SELECT 1 FROM logical_boxes b WHERE b.account_id=q.account_id AND b.name='once-'||replace(q.id::text,'-',''))`, p.AccountID, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if n, _ := result.RowsAffected(); n != 1 {
		writeError(w, 409, fmt.Errorf("run already claimed or unavailable; inspect its box before stopping work"))
		return
	}
	s.listRunOnce(w, r, p)
}

// A small durable submission queue, not an agent orchestrator. Row locking
// serializes claim/cancel. A deterministic box name recovers a committed box
// reservation after a lost response; the process journal handles launch once.
func (s *Server) reconcileRunOnce(ctx context.Context) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id, account, user string
	var data []byte
	err = tx.QueryRowContext(ctx, `SELECT id::text,account_id::text,user_id::text,request FROM run_once_requests WHERE state='queued' ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &account, &user, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var req runOnceRequest
	if err = json.Unmarshal(data, &req); err != nil {
		return err
	}
	p := Principal{AccountID: account, UserID: user, Role: "owner"}
	name := "once-" + strings.ReplaceAll(id, "-", "")
	var boxID string
	err = s.Store.DB.QueryRowContext(ctx, `SELECT id::text FROM logical_boxes WHERE account_id=$1 AND owner_user_id=$2 AND name=$3`, account, user, name).Scan(&boxID)
	var creation *logicalBoxCreation
	if errors.Is(err, sql.ErrNoRows) {
		if err = s.validateRunOnce(ctx, p, req); err != nil {
			_, e := tx.ExecContext(ctx, `UPDATE run_once_requests SET state='blocked',failure=$2 WHERE id=$1`, id, err.Error())
			if e != nil {
				return e
			}
			return tx.Commit()
		}
		allocate := true
		c, e := s.Store.BeginLogicalBoxCreation(ctx, p, v1.CreateLogicalBoxRequest{Name: name, Provider: req.Provider, ProviderCredential: req.ProviderCredential, DiskGiB: 10, DefaultAgent: "shell", LoginProfiles: req.LoginProfiles, AllocateWhenReady: &allocate, AllocationRequestKey: "once-allocate:" + id})
		if errors.Is(e, errNoCreationSlot) {
			return nil
		}
		if e != nil {
			return e
		}
		creation = &c
		boxID = c.Assignment.Box.ID
	} else if err != nil {
		return err
	}
	prompt, err := s.runOnceImagePrompt(ctx, account, req, true)
	if err != nil {
		return err
	}
	task := v1.ProcessTask{ID: id, LogicalBoxID: boxID, BoxName: name, Agent: req.Agent, Prompt: prompt, Model: req.Model, Args: req.Args, Tools: req.Tools, SetupScript: req.SetupScript, Session: "task-" + id, State: "queued", CreatedAt: time.Now().UTC()}
	result, _ := json.Marshal(task)
	if _, err = tx.ExecContext(ctx, `INSERT INTO process_tasks(id,account_id,logical_box_id,user_id,requested_role,idempotency_key,state,result) VALUES($1,$2,$3,$4,'owner',$5,'queued',$6) ON CONFLICT(id) DO NOTHING`, id, account, boxID, user, "run-once:"+id, result); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE run_once_requests SET state='submitted',box_id=$2 WHERE id=$1`, id, boxID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if creation != nil {
		go func() {
			if err := s.finishLogicalBoxCreation(context.Background(), *creation); err != nil {
				s.Logger.Warn("run-once box initialization pending", "box", boxID)
			}
		}()
	}
	return nil
}
