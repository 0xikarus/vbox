package controller

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Server) createProcessHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var req v1.CreateBoxTaskRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if (req.Agent != "codex" && req.Agent != "claude" && req.Agent != "opencode" && req.Agent != "shell") || strings.TrimSpace(req.Prompt) == "" || len(req.Prompt) > 100000 {
		writeError(w, 400, fmt.Errorf("agent codex, claude, opencode, or shell and a prompt (1–100000 bytes) required"))
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if err := v1.ValidateSetupScript(req.SetupScript); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := v1.ValidateTools(req.Tools); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := v1.ValidateProcessOptions(req.Agent, req.Model, req.Args); err != nil {
		writeError(w, 400, err)
		return
	}
	if key == "" {
		writeError(w, 400, fmt.Errorf("Idempotency-Key required"))
		return
	}
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM logical_boxes WHERE id=$1 AND account_id=$2 FOR UPDATE`, box.ID, p.AccountID).Scan(&id); err != nil {
		writeError(w, 409, err)
		return
	}
	var existing []byte
	err = tx.QueryRowContext(r.Context(), `SELECT result FROM process_tasks WHERE account_id=$1 AND idempotency_key=$2`, p.AccountID, key).Scan(&existing)
	if err == nil {
		var task v1.ProcessTask
		_ = json.Unmarshal(existing, &task)
		if task.LogicalBoxID != box.ID || task.Agent != req.Agent || task.Prompt != req.Prompt || task.Model != req.Model || task.SetupScript != req.SetupScript || !slices.Equal(task.Args, req.Args) || !slices.Equal(task.Tools, req.Tools) || (req.Session != "" && task.Session != req.Session) {
			writeError(w, 409, fmt.Errorf("idempotency key belongs to a different request"))
			return
		}
		writeJSON(w, 200, task)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 500, err)
		return
	}
	task := v1.ProcessTask{ID: uuid(), LogicalBoxID: box.ID, BoxName: box.Name, Agent: req.Agent, Prompt: req.Prompt, Model: req.Model, Args: req.Args, Tools: req.Tools, SetupScript: req.SetupScript, State: "queued", CreatedAt: time.Now().UTC()}
	task.Session = req.Session
	if task.Session == "" {
		task.Session = "task-" + task.ID
	}
	if !validSessionName(task.Session) {
		writeError(w, 400, fmt.Errorf("invalid session name"))
		return
	}
	b, _ := json.Marshal(task)
	_, err = tx.ExecContext(r.Context(), `INSERT INTO process_tasks(id,account_id,logical_box_id,user_id,requested_role,idempotency_key,state,result) VALUES($1,$2,$3,$4,$5,$6,'queued',$7)`, task.ID, p.AccountID, box.ID, p.UserID, p.Role, key, b)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, 202, task)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_ = s.reconcileProcess(ctx, p, task.ID)
	}()
}

func (s *Server) processResultHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT t.result FROM process_tasks t JOIN logical_boxes b ON b.id=t.logical_box_id WHERE t.account_id=$1 AND (b.owner_user_id=$2 OR $3='owner') AND (t.id::text=$4 OR b.id::text=$4 OR b.name=$4) ORDER BY t.created_at,t.id`, p.AccountID, p.UserID, p.Role, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer rows.Close()
	values := []v1.ProcessTask{}
	for rows.Next() {
		var b []byte
		var t v1.ProcessTask
		if err = rows.Scan(&b); err != nil {
			writeError(w, 500, err)
			return
		}
		if err = json.Unmarshal(b, &t); err != nil {
			writeError(w, 500, err)
			return
		}
		values = append(values, t)
	}
	if err = rows.Err(); err != nil {
		writeError(w, 500, err)
		return
	}
	if strings.Contains(r.URL.Path, "/logical-boxes/") {
		for i := range values {
			values[i].Output = ""
		}
		writeJSON(w, 200, values)
		return
	}
	if len(values) != 1 {
		writeError(w, 404, fmt.Errorf("process task not found"))
		return
	}
	if strings.HasSuffix(r.URL.Path, "/output") {
		writeJSON(w, 200, map[string]any{"taskId": values[0].ID, "output": values[0].Output, "truncated": values[0].OutputTruncated})
		return
	}
	values[0].Output = ""
	writeJSON(w, 200, values[0])
}

func (s *Server) ReconcileProcessesNow(ctx context.Context) error {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT account_id::text,user_id::text,requested_role,id::text FROM process_tasks WHERE NOT auto_checked ORDER BY created_at,id LIMIT 128`)
	if err != nil {
		return err
	}
	type entry struct {
		p  Principal
		id string
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.p.AccountID, &e.p.UserID, &e.p.Role, &e.id); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var failures []error
	for _, e := range entries {
		c, cancel := context.WithTimeout(ctx, 75*time.Second)
		err = s.reconcileProcess(c, e.p, e.id)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("process %s: %w", e.id, err))
		}
	}
	return errors.Join(failures...)
}

func (s *Server) reconcileProcess(ctx context.Context, p Principal, id string) error {
	var data []byte
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT result FROM process_tasks WHERE account_id=$1 AND id=$2`, p.AccountID, id).Scan(&data); err != nil {
		return err
	}
	var task v1.ProcessTask
	if err := json.Unmarshal(data, &task); err != nil {
		return err
	}
	box, err := s.Store.LogicalBox(ctx, p, task.LogicalBoxID)
	if err != nil {
		return err
	}
	if task.State == "queued" && (box.State == v1.LogicalBoxDetached || box.State == v1.LogicalBoxHibernated) {
		a, err := s.Store.ReserveAllocation(ctx, p, box.ID, "process-allocation:"+id, "task:"+id, 2*time.Minute)
		if err != nil {
			return err
		}
		if a.State == "queued" {
			return nil
		}
		if a.State != "ready" {
			return s.activateAllocation(ctx, p.AccountID, a, a.State == "attaching")
		}
	}
	if box.State != v1.LogicalBoxRunning {
		return nil
	}
	a, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil {
		return err
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		return err
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT assignment_generation FROM logical_boxes WHERE id=$1 AND account_id=$2 AND state='running' AND fencing_token=$3 FOR UPDATE`, box.ID, p.AccountID, a.FencingToken).Scan(&generation); err != nil {
		return err
	}
	if generation != a.Box.AssignmentGeneration {
		return fmt.Errorf("assignment changed")
	}
	if err = tx.QueryRowContext(ctx, `SELECT result FROM process_tasks WHERE account_id=$1 AND id=$2 FOR UPDATE`, p.AccountID, id).Scan(&data); err != nil {
		return err
	}
	if err = json.Unmarshal(data, &task); err != nil {
		return err
	}
	if task.State == "queued" {
		if err = stageWorkspaceRuntime(ctx, prov, a.Slot.ServiceID, s.WorkerRuntime); err != nil {
			return err
		}
		encoded, _ := json.Marshal(task)
		if err = s.provisionDesktopAgent(ctx, tx, p, a, prov); err != nil {
			return err
		}
		result, e := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "process-start", base64.RawURLEncoding.EncodeToString(encoded)}, provider.ExecOptions{})
		// Even an SSH error can follow a successful start. Persist unknown and
		// collect the journal later, never infer failure or replay the prompt.
		task.State = "starting"
		if e != nil || result.ExitCode != 0 {
			task.State = "unknown"
		}
	}
	if task.FinishedAt == nil {
		result, e := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "process-status", id}, provider.ExecOptions{})
		if e == nil && result.ExitCode == 0 {
			var observed v1.ProcessTask
			if e = json.Unmarshal([]byte(result.Stdout), &observed); e != nil {
				return fmt.Errorf("invalid process result")
			}
			if observed.ID != task.ID || observed.Session != task.Session || observed.Agent != task.Agent || observed.LogicalBoxID != task.LogicalBoxID {
				return fmt.Errorf("mismatched process result")
			}
			task = observed
			if task.FinishedAt == nil { // A missing session without a result is unknown, not completed.
				r, e := prov.Exec(ctx, a.Slot.ServiceID, []string{"tmux", "has-session", "-t", "=" + task.Session}, provider.ExecOptions{})
				if e != nil || r.ExitCode != 0 {
					task.State = "unknown"
				}
			}
		} else {
			task.State = "unknown"
		}
	}
	data, _ = json.Marshal(task)
	if _, err = tx.ExecContext(ctx, `UPDATE process_tasks SET state=$3,result=$4 WHERE account_id=$1 AND id=$2`, p.AccountID, id, task.State, data); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if task.FinishedAt != nil {
		return s.hibernateAfterProcess(ctx, p, a, prov)
	}
	return nil
}

func (s *Server) hibernateAfterProcess(ctx context.Context, p Principal, a fleetAssignment, prov provider.Provider) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT assignment_generation FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state='running' AND fencing_token=$3 FOR UPDATE`, p.AccountID, a.Box.ID, a.FencingToken).Scan(&generation); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if generation != a.Box.AssignmentGeneration {
		return fmt.Errorf("assignment changed")
	}
	var busy bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM process_tasks WHERE logical_box_id=$1 AND result->>'finishedAt' IS NULL) OR EXISTS(SELECT 1 FROM box_tasks WHERE logical_box_id=$1 AND state IN ('queued','waiting_capacity','starting','active'))`, a.Box.ID).Scan(&busy)
	if err != nil || busy {
		return err
	}
	r, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "native-sessions", nativeFence(a)}, provider.ExecOptions{})
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("cannot prove idle worker")
	}
	var inv v1.SessionInventory
	if err = json.Unmarshal([]byte(r.Stdout), &inv); err != nil {
		return err
	}
	if inv.State != "live" || inv.Assignment != nativeFence(a) || inv.Partial || len(inv.Sessions) != 0 {
		return nil
	}
	// Process tasks on a persistent box retain the workspace after hibernation.
	_, err = tx.ExecContext(ctx, `UPDATE logical_boxes SET state='hibernating',lease_owner=NULL,lease_expires_at=NULL,restoration_state='auto-hibernate-queued',metadata=jsonb_set(metadata,'{lastStop}','"idle"'::jsonb,true),failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, a.Box.ID)
	if err != nil {
		return err
	}
	rr, err := tx.ExecContext(ctx, `UPDATE compute_slots SET state='draining',lease_expires_at=now()+interval '5 minutes',updated_at=now() WHERE account_id=$1 AND id=$2 AND assignment_generation=$3 AND fencing_token=$4 AND state='occupied'`, p.AccountID, a.Slot.ID, generation, a.FencingToken)
	if err != nil {
		return err
	}
	if n, _ := rr.RowsAffected(); n != 1 {
		return fmt.Errorf("slot assignment changed")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE process_tasks SET auto_checked=true WHERE logical_box_id=$1 AND result->>'finishedAt' IS NOT NULL`, a.Box.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.startLogicalBoxHibernate(p, a.Box.ID)
	return nil
}

func (s *Server) interactiveStartHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var req struct {
		Agent         string `json:"agent"`
		StartCLI      string `json:"startCli,omitempty"`
		ReuseShell    bool   `json:"reuseShell,omitempty"`
		ReuseAgent    bool   `json:"reuseAgent,omitempty"`
		ReuseExisting bool   `json:"reuseExisting,omitempty"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if req.Agent != "codex" && req.Agent != "claude" && req.Agent != "opencode" && req.Agent != "shell" {
		writeError(w, 400, fmt.Errorf("choose codex, claude, opencode, or shell"))
		return
	}
	if len(req.StartCLI) > 16384 || strings.ContainsRune(req.StartCLI, 0) || ((req.StartCLI != "" || req.ReuseShell) && req.Agent != "shell") || ((req.ReuseShell || req.ReuseAgent || req.ReuseExisting) && req.StartCLI != "") {
		writeError(w, 400, fmt.Errorf("startCli requires a new shell; reuseShell cannot replay a startup command"))
		return
	}
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	a, err := s.Store.assignment(r.Context(), p.AccountID, box.ID)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer tx.Rollback()
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT assignment_generation FROM logical_boxes WHERE id=$1 AND account_id=$2 AND state='running' AND fencing_token=$3 FOR UPDATE`, box.ID, p.AccountID, a.FencingToken).Scan(&generation); err != nil || generation != a.Box.AssignmentGeneration {
		writeError(w, 409, fmt.Errorf("box assignment changed; reconnect"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	if err = stageWorkspaceRuntime(ctx, prov, a.Slot.ServiceID, s.WorkerRuntime); err != nil {
		writeError(w, 502, err)
		return
	}
	if !req.ReuseExisting {
		if err = s.provisionDesktopAgent(ctx, tx, p, a, prov); err != nil {
			writeError(w, 502, err)
			return
		}
	}
	if req.ReuseAgent || req.ReuseExisting {
		var remembered, agent string
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(metadata->>'primarySession',''),COALESCE(metadata->>'primaryAgent','') FROM logical_boxes WHERE account_id=$1 AND id=$2`, p.AccountID, box.ID).Scan(&remembered, &agent); err != nil {
			writeError(w, 500, fmt.Errorf("managed session unavailable"))
			return
		}
		result, inspectErr := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "native-sessions", nativeFence(a)}, provider.ExecOptions{})
		var inv v1.SessionInventory
		if inspectErr != nil || result.ExitCode != 0 || json.Unmarshal([]byte(result.Stdout), &inv) != nil || inv.State != "live" || inv.Assignment != nativeFence(a) || inv.Partial {
			writeError(w, 409, fmt.Errorf("managed session inventory unavailable"))
			return
		}
		if req.ReuseExisting {
			if name := reusableInteractiveSession(inv.Sessions, remembered); name != "" {
				if tx.Commit() != nil {
					writeError(w, 409, fmt.Errorf("session observation changed"))
					return
				}
				writeJSON(w, 200, map[string]string{"session": name})
				return
			}
		}
		for _, existing := range inv.Sessions {
			if !req.ReuseExisting && existing.Name == remembered && agent == req.Agent {
				if tx.Commit() != nil {
					writeError(w, 409, fmt.Errorf("session observation changed"))
					return
				}
				writeJSON(w, 200, map[string]string{"session": remembered})
				return
			}
		}
	}
	if req.ReuseShell {
		var remembered string
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(metadata->>'shellSession', CASE WHEN metadata->>'primarySession' LIKE 'shell-%' THEN metadata->>'primarySession' END, '') FROM logical_boxes WHERE account_id=$1 AND id=$2`, p.AccountID, box.ID).Scan(&remembered); err != nil {
			writeError(w, 500, err)
			return
		}
		result, inspectErr := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "native-sessions", nativeFence(a)}, provider.ExecOptions{})
		var inv v1.SessionInventory
		if inspectErr != nil || result.ExitCode != 0 || json.Unmarshal([]byte(result.Stdout), &inv) != nil || inv.State != "live" || inv.Assignment != nativeFence(a) || inv.Partial {
			writeError(w, 502, fmt.Errorf("shell inventory unconfirmed; no session created"))
			return
		}
		for _, existing := range inv.Sessions {
			if existing.Name == remembered {
				if err = tx.Commit(); err != nil {
					writeError(w, 409, err)
					return
				}
				writeJSON(w, 200, map[string]string{"session": remembered})
				return
			}
		}
	}
	if req.ReuseExisting {
		if err = s.provisionDesktopAgent(ctx, tx, p, a, prov); err != nil {
			writeError(w, 502, err)
			return
		}
	}
	// The managed harness requests its MCP policy during startup. Publish the
	// desktop credential before launching it, or that request sees an unknown
	// token until this transaction finally commits.
	if err = tx.Commit(); err != nil {
		writeError(w, 409, fmt.Errorf("desktop credential could not be committed"))
		return
	}
	session := generatedTaskSession(req.Agent)
	argv := []string{"vmbox-runtime", "interactive-start", nativeFence(a), session, req.Agent}
	if req.StartCLI != "" {
		argv = append(argv, req.StartCLI)
	}
	result, err := prov.Exec(ctx, a.Slot.ServiceID, argv, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		writeError(w, 502, fmt.Errorf("interactive startup unconfirmed; inspect sessions before retrying"))
		return
	}
	updated, updateErr := s.Store.DB.ExecContext(ctx, `UPDATE logical_boxes SET metadata=jsonb_set(jsonb_set(metadata,'{primarySession}',to_jsonb($3::text)),'{primaryAgent}',to_jsonb($4::text)),updated_at=now() WHERE account_id=$1 AND id=$2 AND state='running' AND fencing_token=$5 AND assignment_generation=$6`, p.AccountID, box.ID, session, req.Agent, a.FencingToken, a.Box.AssignmentGeneration)
	if updateErr != nil {
		writeError(w, 500, fmt.Errorf("managed session started but identity could not be saved"))
		return
	}
	rows, rowsErr := updated.RowsAffected()
	if rowsErr != nil || rows != 1 {
		writeError(w, 409, fmt.Errorf("managed session started but assignment changed; inspect sessions before retrying"))
		return
	}
	if req.Agent == "shell" {
		updated, updateErr = s.Store.DB.ExecContext(ctx, `UPDATE logical_boxes SET metadata=jsonb_set(metadata,'{shellSession}',to_jsonb($3::text)),updated_at=now() WHERE account_id=$1 AND id=$2 AND state='running' AND fencing_token=$4 AND assignment_generation=$5`, p.AccountID, box.ID, session, a.FencingToken, a.Box.AssignmentGeneration)
		if updateErr != nil {
			writeError(w, 500, fmt.Errorf("shell started but its identity could not be saved; inspect sessions before retrying"))
			return
		}
		rows, rowsErr = updated.RowsAffected()
		if rowsErr != nil || rows != 1 {
			writeError(w, 409, fmt.Errorf("shell started but assignment changed; inspect sessions before retrying"))
			return
		}
	}
	writeJSON(w, 201, map[string]string{"session": session})
}
