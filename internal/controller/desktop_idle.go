package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type idleReleaseCheckKey struct{}
type idleReleaseCheck func(context.Context, *sql.Tx, fleetAssignment) error

func (s *Server) desktopIdlePolicy(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	var policy struct {
		Seconds       int `json:"seconds"`
		ResumeSeconds int `json:"resumeSeconds"`
	}
	if r.Method == "PUT" {
		if decodeJSON(r, &policy) != nil || policy.Seconds < 0 || policy.Seconds > 604800 || (policy.Seconds > 0 && policy.Seconds < 60) {
			writeError(w, 400, fmt.Errorf("choose 0 to disable or 60–604800 seconds"))
			return
		}
		err = s.Store.DB.QueryRowContext(r.Context(), `UPDATE logical_boxes SET
metadata=jsonb_set(metadata,'{idleTimeoutSavedSeconds}',to_jsonb(CASE WHEN $3>0 THEN $3 WHEN idle_timeout_seconds>0 THEN idle_timeout_seconds ELSE COALESCE(CASE WHEN (metadata->>'idleTimeoutSavedSeconds') ~ '^[0-9]{1,6}$' THEN (metadata->>'idleTimeoutSavedSeconds')::integer END,14400) END),true),
idle_timeout_seconds=$3,updated_at=now() WHERE account_id=$1 AND id=$2
RETURNING idle_timeout_seconds,(metadata->>'idleTimeoutSavedSeconds')::integer`, p.AccountID, box.ID, policy.Seconds).Scan(&policy.Seconds, &policy.ResumeSeconds)
	} else {
		err = s.Store.DB.QueryRowContext(r.Context(), `SELECT idle_timeout_seconds,COALESCE(CASE WHEN (metadata->>'idleTimeoutSavedSeconds') ~ '^[0-9]{1,6}$' THEN (metadata->>'idleTimeoutSavedSeconds')::integer END,NULLIF(idle_timeout_seconds,0),14400) FROM logical_boxes WHERE account_id=$1 AND id=$2`, p.AccountID, box.ID).Scan(&policy.Seconds, &policy.ResumeSeconds)
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("idle policy unavailable"))
		return
	}
	writeJSON(w, 200, policy)
}

func (s *Server) ReconcileDesktopIdleNow(ctx context.Context) error {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT account_id::text,id::text,owner_user_id::text FROM logical_boxes WHERE state='running' AND idle_timeout_seconds>0 AND updated_at < now()-make_interval(secs=>idle_timeout_seconds) ORDER BY updated_at LIMIT 32`)
	if err != nil {
		return err
	}
	var candidates []pendingLogicalBoxHibernate
	for rows.Next() {
		var value pendingLogicalBoxHibernate
		if err = rows.Scan(&value.AccountID, &value.BoxID, &value.OwnerUserID); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, value := range candidates {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p := Principal{AccountID: value.AccountID, UserID: value.OwnerUserID, Role: "user", Subject: "controller:desktop-idle"}
		check := idleReleaseCheck(func(ctx context.Context, tx *sql.Tx, a fleetAssignment) error {
			seconds, eligible, err := desktopIdleEligible(ctx, tx, a.Box.ID, p.AccountID)
			if err != nil || !eligible {
				return fmt.Errorf("box is active or idle policy changed")
			}
			prov, err := s.provider(ctx, p.AccountID, a.Box.Provider, a.Box.ProviderCredential)
			if err != nil {
				return err
			}
			result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "desktop-idle", nativeFence(a)}, provider.ExecOptions{})
			var observed struct {
				Seconds int64 `json:"seconds"`
			}
			if err != nil || result.ExitCode != 0 || len(result.Stdout) > 1024 || json.Unmarshal([]byte(result.Stdout), &observed) != nil || observed.Seconds < int64(seconds) {
				return fmt.Errorf("desktop is active or idle observation unavailable")
			}
			return nil
		})
		attempt, cancel := context.WithTimeout(context.WithValue(ctx, idleReleaseCheckKey{}, check), 15*time.Second)
		_, err := s.Store.BeginLogicalBoxRelease(attempt, p, value.BoxID, v1.LogicalBoxHibernating)
		cancel()
		if err == nil {
			s.startLogicalBoxHibernate(p, value.BoxID)
		}
	}
	return nil
}

func desktopIdleEligible(ctx context.Context, db desktopSecretQuerier, box, account string) (int, bool, error) {
	var seconds int
	var eligible bool
	err := db.QueryRowContext(ctx, `SELECT idle_timeout_seconds,
    state='running' AND idle_timeout_seconds>0 AND updated_at < now()-make_interval(secs=>idle_timeout_seconds)
    AND NOT EXISTS(SELECT 1 FROM box_tasks WHERE logical_box_id=$1 AND state IN ('queued','waiting_capacity','starting','active'))
    AND NOT EXISTS(SELECT 1 FROM process_tasks WHERE logical_box_id=$1 AND state IN ('queued','starting','running'))
    AND NOT EXISTS(SELECT 1 FROM desktop_secret_requests WHERE box_id=$1 AND status='pending')
    FROM logical_boxes WHERE id=$1 AND account_id=$2`, box, account).Scan(&seconds, &eligible)
	return seconds, eligible, err
}
