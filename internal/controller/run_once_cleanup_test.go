package controller

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestRunOnceArchivesResultBeforeDeletingBox(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.DB.SetMaxOpenConns(1)
	schema := "once_cleanup_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = s.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer s.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	if _, err = s.DB.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := s.Bootstrap(ctx, "cleanup", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	box, slot, id := uuid(), uuid(), uuid()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.DB.ExecContext(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO compute_slots(id,account_id,provider,ordinal,state,service_id,assignment_generation,fencing_token) VALUES($1,$2,'railway',1,'occupied','service',1,'fence')`, slot, p.AccountID)
	exec(`INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,slot_id,assignment_generation,fencing_token) VALUES($1,$2,$3,'once-test','railway','running','volume','volume',$4,1,'fence')`, box, p.AccountID, p.UserID, slot)
	now, code := time.Now().UTC(), 7
	task := v1.ProcessTask{ID: id, LogicalBoxID: box, Agent: "shell", Session: "task-test", State: "exited", FinishedAt: &now, ExitCode: &code, Output: "retained output ä\n"}
	data, _ := json.Marshal(task)
	exec(`INSERT INTO process_tasks(id,account_id,logical_box_id,user_id,requested_role,idempotency_key,state,result) VALUES($1,$2,$3,$4,'owner','test','exited',$5)`, id, p.AccountID, box, p.UserID, data)
	exec(`INSERT INTO run_once_requests(id,account_id,user_id,request_key,request,box_id,state) VALUES($1,$2,$3,'test','{}',$4,'submitted')`, id, p.AccountID, p.UserID, box)
	a := fleetAssignment{Box: v1.LogicalBox{ID: box, AssignmentGeneration: 1}, Slot: v1.ComputeSlot{ID: slot, ServiceID: "service"}, FencingToken: "fence"}
	server := NewServer(s, provider.NewRegistry())
	started := make(chan string, 1)
	server.StartDelete = func(_ context.Context, _ Principal, boxID string) error { started <- boxID; return nil }
	prov := &idleProcessProvider{inv: v1.SessionInventory{State: "live", Assignment: nativeFence(a), Sessions: []v1.Session{{Name: "sibling"}}}}
	if err = server.hibernateAfterProcess(ctx, p, a, prov); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = s.DB.QueryRowContext(ctx, `SELECT state FROM logical_boxes WHERE id=$1`, box).Scan(&state); err != nil || state != "running" {
		t.Fatal("sibling not preserved", state, err)
	}
	prov.inv.Sessions = nil
	if err = server.hibernateAfterProcess(ctx, p, a, prov); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-started:
		if got != box {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal("missing deletion handoff")
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT state FROM logical_boxes WHERE id=$1`, box).Scan(&state); err != nil || state != "deleting" {
		t.Fatal(state, err)
	}
	// Simulate final provider-confirmed removal; process_tasks cascades away.
	exec(`DELETE FROM logical_boxes WHERE id=$1`, box)
	r := httptest.NewRequest("GET", "/v1/run-once/"+id, nil)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	server.listRunOnce(w, r, p)
	var saved runOnceRecord
	if err = json.Unmarshal(w.Body.Bytes(), &saved); err != nil || w.Code != 200 || !saved.BoxDeleted || saved.Task == nil {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	if saved.Task.ExitCode == nil || *saved.Task.ExitCode != 7 || saved.Task.Output != task.Output {
		t.Fatal("result lost", saved.Task)
	}
	other := p
	other.AccountID = uuid()
	w = httptest.NewRecorder()
	server.listRunOnce(w, r, other)
	if w.Code != 404 {
		t.Fatal("cross-account result leak", w.Code)
	}
}
