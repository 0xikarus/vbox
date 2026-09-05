package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func nativeFence(a fleetAssignment) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", a.Box.ID, a.Box.AssignmentGeneration, a.FencingToken))))
}

// Explicit, owner-only migration for already running boxes. Serialize against
// assignment changes while staging the matching runtime and binding the live
// tmux server; no session, process, or worker restart is requested.
func (s *Server) enableNativeHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	a, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer tx.Rollback()
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT assignment_generation FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state='running' AND slot_id=$3 AND fencing_token=$4 FOR UPDATE`, p.AccountID, box.ID, a.Slot.ID, a.FencingToken).Scan(&generation)
	if err != nil || generation != a.Box.AssignmentGeneration {
		writeError(w, 409, fmt.Errorf("assignment changed or box not running"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	if len(s.WorkerRuntime) == 0 {
		writeError(w, 409, fmt.Errorf("matching worker runtime unavailable"))
		return
	}
	if err = stageWorkspaceRuntime(ctx, prov, a.Slot.ServiceID, s.WorkerRuntime); err != nil {
		writeError(w, 502, fmt.Errorf("worker runtime staging failed"))
		return
	}
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "native-bind", nativeFence(a)}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		writeError(w, 502, fmt.Errorf("native session migration failed"))
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"enabled": true})
}

func (s *Server) observeSessions(ctx context.Context, p Principal, id string) (v1.SessionInventory, error) {
	box, err := s.Store.LogicalBox(ctx, p, id)
	if err != nil {
		return v1.SessionInventory{}, err
	}
	out := v1.SessionInventory{LogicalBoxID: box.ID, State: "offline", ObservedAt: time.Now().UTC(), Sessions: []v1.Session{}}
	if box.State != v1.LogicalBoxRunning {
		return out, nil
	}
	a, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil {
		return out, err
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "native-sessions", nativeFence(a)}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		return out, fmt.Errorf("session observation unavailable; worker runtime or assignment may need migration (not session completion)")
	}
	if err := json.Unmarshal([]byte(result.Stdout), &out); err != nil {
		return out, fmt.Errorf("invalid worker session inventory")
	}
	current, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || nativeFence(current) != nativeFence(a) || current.Box.State != v1.LogicalBoxRunning || out.Assignment != nativeFence(a) {
		return out, fmt.Errorf("assignment changed during observation")
	}
	out.LogicalBoxID = box.ID
	return out, nil
}

func (s *Server) sessionsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	out, err := s.observeSessions(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 502, err)
		return
	}
	tasks, err := s.Store.ListBoxTasks(r.Context(), p, out.LogicalBoxID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	for i := range out.Sessions {
		for j := len(tasks) - 1; j >= 0; j-- {
			task := tasks[j]
			if task.Session == out.Sessions[i].Name {
				out.Sessions[i].TaskID = task.ID
				out.Sessions[i].TaskAgent = task.Agent
				out.Sessions[i].TaskState = task.State
				break
			}
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) nativeWelcomeHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	if box.State != v1.LogicalBoxRunning {
		writeError(w, 409, fmt.Errorf("box is not running"))
		return
	}
	a, err := s.Store.assignment(r.Context(), p.AccountID, box.ID)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	prov, err := s.provider(r.Context(), p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	// Old clients may still request a welcome session. Serialize that launch
	// against the same box lock used by automatic one-shot hibernation.
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer tx.Rollback()
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT assignment_generation FROM logical_boxes WHERE id=$1 AND account_id=$2 AND state='running' AND fencing_token=$3 FOR UPDATE`, box.ID, p.AccountID, a.FencingToken).Scan(&generation); err != nil || generation != a.Box.AssignmentGeneration {
		writeError(w, 409, fmt.Errorf("assignment changed"))
		return
	}
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "native-welcome", nativeFence(a)}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		writeError(w, 409, fmt.Errorf("welcome session could not be created; refresh session inventory"))
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 409, err)
		return
	}
	s.sessionsHandler(w, r, p)
}

func (s *Server) nativeConnectionHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	inv, err := s.observeSessions(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 502, err)
		return
	}
	var selected *v1.Session
	for i := range inv.Sessions {
		if inv.Sessions[i].ID == r.URL.Query().Get("sessionId") && inv.Sessions[i].Incarnation == r.URL.Query().Get("incarnation") {
			selected = &inv.Sessions[i]
			break
		}
	}
	if selected == nil {
		writeError(w, 409, fmt.Errorf("selected session disappeared or was recreated"))
		return
	}
	a, err := s.Store.assignment(r.Context(), p.AccountID, inv.LogicalBoxID)
	if err != nil || nativeFence(a) != inv.Assignment || a.Box.State != v1.LogicalBoxRunning {
		writeError(w, 409, fmt.Errorf("assignment changed"))
		return
	}
	prov, err := s.provider(r.Context(), p.AccountID, a.Box.Provider, a.Box.ProviderCredential)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	conn, err := prov.Connection(r.Context(), a.Slot.ServiceID)
	if err != nil {
		writeError(w, 502, fmt.Errorf("connection resolution failed"))
		return
	}
	if conn.Metadata["deploymentInstanceId"] == "" || conn.Metadata["deploymentInstanceId"] != a.Slot.DeploymentInstanceID {
		writeError(w, 409, fmt.Errorf("deployment changed"))
		return
	}
	writeJSON(w, 200, v1.NativeConnection{LogicalBoxConnection: v1.LogicalBoxConnection{LogicalBoxID: a.Box.ID, BoxName: a.Box.Name, Session: selected.Name, Connection: conn}, Assignment: inv.Assignment, SessionID: selected.ID, Incarnation: selected.Incarnation})
}
