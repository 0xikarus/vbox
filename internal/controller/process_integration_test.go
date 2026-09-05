package controller

import (
	"context"
	"encoding/json"
	"errors"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type idleProcessProvider struct {
	fakeProvider
	inv   v1.SessionInventory
	fail  bool
	calls int
}

func (p *idleProcessProvider) Exec(_ context.Context, _ string, args []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	p.calls++
	if p.fail {
		return provider.ExecResult{}, errors.New("SSH permission denied")
	}
	b, _ := json.Marshal(p.inv)
	return provider.ExecResult{Stdout: string(b)}, nil
}

func TestProcessPostgres(t *testing.T) {
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
	schema := "process_test_" + strings.ReplaceAll(uuid(), "-", "")
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
	s.Envelope, _ = secrets.New(make([]byte, 32))
	p, err := s.Bootstrap(ctx, "process-test", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	box, slot := uuid(), uuid()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,ordinal,state,service_id,assignment_generation,fencing_token) VALUES($1,$2,'railway',1,'occupied','test-service',1,'fence')`, slot, p.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,slot_id,assignment_generation,fencing_token) VALUES($1,$2,$3,'process-box','railway','reserved','test-volume','test-volume',$4,1,'fence')`, box, p.AccountID, p.UserID, slot)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(s, provider.NewRegistry())
	create := func(prompt string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"agent":"shell","prompt":"`+prompt+`"}`))
		r.SetPathValue("id", box)
		r.Header.Set("Idempotency-Key", "test-key")
		w := httptest.NewRecorder()
		server.createProcessHandler(w, r, p)
		return w
	}
	w := create("exit 7")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var task v1.ProcessTask
	if err = json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	w = create("exit 7")
	if w.Code != 200 {
		t.Fatal("retry", w.Code, w.Body.String())
	}
	var retry v1.ProcessTask
	json.Unmarshal(w.Body.Bytes(), &retry)
	if retry.ID != task.ID {
		t.Fatal("duplicated task")
	}
	if w = create("exit 0"); w.Code != 409 {
		t.Fatal("different prompt reused key", w.Code)
	}
	// No queued task may be shut down, including one committed concurrently
	// before the idle decision acquires its box row lock.
	_, err = s.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='running' WHERE id=$1`, box)
	if err != nil {
		t.Fatal(err)
	}
	a := fleetAssignment{Box: v1.LogicalBox{ID: box, AssignmentGeneration: 1}, Slot: v1.ComputeSlot{ID: slot, ServiceID: "test-service"}, FencingToken: "fence"}
	t.Run("interactive shell reuse", func(t *testing.T) { testInteractiveShellReuse(t, server, p, box) })
	prov := &idleProcessProvider{inv: v1.SessionInventory{State: "live", Assignment: nativeFence(a), Sessions: []v1.Session{}}}
	if err = server.hibernateAfterProcess(ctx, p, a, prov); err != nil || prov.calls != 0 {
		t.Fatal("queued guard", err, prov.calls)
	}
	now := time.Now().UTC()
	code := 7
	task.State = "exited"
	task.StartedAt = &now
	task.FinishedAt = &now
	task.ExitCode = &code
	task.Output = "real retained output"
	b, _ := json.Marshal(task)
	if _, err = s.DB.ExecContext(ctx, `UPDATE process_tasks SET state='exited',result=$2 WHERE id=$1`, task.ID, b); err != nil {
		t.Fatal(err)
	}
	prov.inv.Sessions = []v1.Session{{ID: "$1", Name: "interactive"}}
	if err = server.hibernateAfterProcess(ctx, p, a, prov); err != nil {
		t.Fatal(err)
	}
	assertRunning := func() {
		var state string
		if err := s.DB.QueryRowContext(ctx, `SELECT state FROM logical_boxes WHERE id=$1`, box).Scan(&state); err != nil || state != "running" {
			t.Fatal(state, err)
		}
	}
	assertRunning()
	prov.inv.Sessions = nil
	prov.inv.Partial = true
	if err = server.hibernateAfterProcess(ctx, p, a, prov); err != nil {
		t.Fatal(err)
	}
	assertRunning()
	prov.inv.Partial = false
	prov.fail = true
	if err = server.hibernateAfterProcess(ctx, p, a, prov); err == nil {
		t.Fatal("SSH failure ignored")
	}
	assertRunning()
	prov.fail = false
	// A newly queued sibling blocks even when the last sampled terminal is empty.
	sibling := uuid()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO process_tasks(id,account_id,logical_box_id,user_id,requested_role,idempotency_key,state,result) VALUES($1,$2,$3,$4,'owner','sibling','unknown','{}')`, sibling, p.AccountID, box, p.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.hibernateAfterProcess(ctx, p, a, prov); err != nil {
		t.Fatal(err)
	}
	assertRunning()
	if _, err = s.DB.ExecContext(ctx, `DELETE FROM process_tasks WHERE id=$1`, sibling); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 1)
	server.StartHibernate = func(_ context.Context, _ Principal, id string) error { started <- id; return nil }
	if err = server.hibernateAfterProcess(ctx, p, a, prov); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-started:
		if id != box {
			t.Fatal(id)
		}
	case <-ctx.Done():
		t.Fatal("no durable hibernate handoff")
	}
	var state string
	var checked bool
	if err = s.DB.QueryRowContext(ctx, `SELECT b.state,t.auto_checked FROM logical_boxes b JOIN process_tasks t ON t.logical_box_id=b.id WHERE t.id=$1`, task.ID).Scan(&state, &checked); err != nil || state != "hibernating" || !checked {
		t.Fatal(state, checked, err)
	}
	// Result reads use database only, including when compute is unavailable.
	r := httptest.NewRequest("GET", "/v1/process-tasks/"+task.ID, nil)
	r.SetPathValue("id", task.ID)
	w = httptest.NewRecorder()
	server.processResultHandler(w, r, p)
	var saved v1.ProcessTask
	if err = json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.ExitCode == nil || *saved.ExitCode != 7 || saved.Output != "" {
		t.Fatal(w.Body.String(), err)
	}
	r = httptest.NewRequest("GET", "/v1/process-tasks/"+task.ID+"/output", nil)
	r.SetPathValue("id", task.ID)
	w = httptest.NewRecorder()
	server.processResultHandler(w, r, p)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "real retained output") {
		t.Fatal(w.Code, w.Body.String())
	}
	other := p
	other.UserID = uuid()
	other.Role = "user"
	w = httptest.NewRecorder()
	server.processResultHandler(w, r, other)
	if w.Code != 404 {
		t.Fatal("foreign task output accessible", w.Code)
	}
}
