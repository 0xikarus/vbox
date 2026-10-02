package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// A disposable schema checks the actual PostgreSQL CASE expressions and
// timestamp semantics that sqlmock cannot evaluate.
func TestMascotObservationQuietAndBusyResetPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.DB.SetMaxOpenConns(1)
	schema := "mascot_observation_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err := store.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer store.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	if _, err := store.DB.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := store.Bootstrap(ctx, "mascot-test", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	boxID, taskID := uuid(), uuid()
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name)
		VALUES($1,$2,$3,'activity-test','railway','running','test-volume','test-volume')`, boxID, p.AccountID, p.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_tasks(id,account_id,logical_box_id,user_id,requested_role,agent,session_name,prompt,state,idempotency_key,agent_busy,agent_busy_updated_at)
		VALUES($1,$2,$3,$4,'owner','codex','activity-test','test','active','activity-test',true,now()-interval '4 minutes')`, taskID, p.AccountID, boxID, p.UserID); err != nil {
		t.Fatal(err)
	}
	server := NewServer(store, nil)
	observe := func(text string) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"session": "activity-test", "text": text})
		request := httptest.NewRequest(http.MethodPost, "/v1/agent-desktop/mascot-observation", bytes.NewReader(body))
		request.SetPathValue("id", boxID)
		response := httptest.NewRecorder()
		server.mascotObservationHandler(response, request, p)
		if response.Code != http.StatusOK {
			t.Fatalf("observation status %d: %s", response.Code, response.Body.String())
		}
	}
	status := func() boxActivity {
		t.Helper()
		response := httptest.NewRecorder()
		server.boxActivityHandler(response, httptest.NewRequest(http.MethodGet, "/v1/box-activity", nil), p)
		if response.Code != http.StatusOK {
			t.Fatalf("activity status %d: %s", response.Code, response.Body.String())
		}
		var values []boxActivity
		if err := json.Unmarshal(response.Body.Bytes(), &values); err != nil || len(values) != 1 {
			t.Fatalf("activity values %+v: %v", values, err)
		}
		return values[0]
	}
	observe("tool: Editing chat.js")
	if got := status(); got.Status != "Editing chat.js" || got.StatusSource != "specific" {
		t.Fatalf("first observation: %+v", got)
	}
	if _, err := store.DB.ExecContext(ctx, `UPDATE box_tasks SET mascot_evidence_changed_at=now()-interval '3 minutes',mascot_phrase_at=now()-interval '3 minutes' WHERE id=$1`, taskID); err != nil {
		t.Fatal(err)
	}
	observe("tool: Editing chat.js")
	if got := status(); got.Status != "Idle" || got.StatusSource != "quiet" || got.Mood != "idle" {
		t.Fatalf("unchanged evidence: %+v", got)
	}
	if _, err := store.DB.ExecContext(ctx, `UPDATE box_tasks SET agent_busy_updated_at=now() WHERE id=$1`, taskID); err != nil {
		t.Fatal(err)
	}
	observe("tool: Editing chat.js")
	if got := status(); got.Status != "Working" || got.StatusSource != "fallback" {
		t.Fatalf("new busy turn: %+v", got)
	}
	observe("tool: Reviewing README.md")
	if got := status(); got.Status != "Reviewing README.md" || got.StatusSource != "specific" {
		t.Fatalf("changed evidence: %+v", got)
	}
}
