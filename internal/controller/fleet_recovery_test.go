package controller

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

// recordingProvider notes the exact order of the lifecycle calls a fleet slot
// receives, which is what the recovery of a stopped slot depends on.
type recordingProvider struct {
	fakeProvider
	state      provider.State
	operations []string
	execArgv   [][]string
	inspectErr error
	startErr   error
}

type runtimeDeadlineProvider struct {
	recordingProvider
	blockInstall bool
	sawDeadline  bool
}

func (p *runtimeDeadlineProvider) Exec(ctx context.Context, id string, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 91*time.Second {
		return provider.ExecResult{}, fmt.Errorf("runtime operation has no bounded deadline")
	}
	p.sawDeadline = true
	if p.blockInstall && opts.Stdin == nil {
		<-ctx.Done()
		return provider.ExecResult{}, ctx.Err()
	}
	return p.recordingProvider.Exec(ctx, id, argv, opts)
}
func TestWorkspaceRuntimeInstallationIsBounded(t *testing.T) {
	p := &runtimeDeadlineProvider{}
	if err := stageWorkspaceRuntime(context.Background(), p, "service", []byte("runtime")); err != nil || !p.sawDeadline {
		t.Fatalf("deadline: %v", err)
	}
	p.blockInstall = true
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := stageWorkspaceRuntime(ctx, p, "service", []byte("runtime")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled install: %v", err)
	}
}

func (p *recordingProvider) Inspect(context.Context, string) (provider.Box, error) {
	p.operations = append(p.operations, "inspect")
	if p.inspectErr != nil {
		return provider.Box{}, p.inspectErr
	}
	return provider.Box{ID: "service-1", State: p.state}, nil
}

func (p *recordingProvider) Start(context.Context, string) (provider.Box, error) {
	p.operations = append(p.operations, "start")
	if p.startErr != nil {
		return provider.Box{}, p.startErr
	}
	p.state = provider.StateRunning
	return provider.Box{ID: "service-1", State: provider.StateRunning}, nil
}

func (p *recordingProvider) Exec(_ context.Context, _ string, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	p.operations = append(p.operations, "exec:"+strings.Join(argv, " "))
	p.execArgv = append(p.execArgv, append([]string(nil), argv...))
	if opts.Stdin != nil {
		data, _ := io.ReadAll(opts.Stdin)
		return provider.ExecResult{Stdout: fmt.Sprintf("%x\n", sha256.Sum256(data))}, nil
	}
	return provider.ExecResult{Stdout: "ok\n"}, nil
}

// TestStoppedInitializationSlotIsStartedBeforeAnyRuntimeProbe pins the fix in
// 108e6a4: a slot the fleet powered down while it was free has no deployment to
// reach, so probing it first reports a broken workspace that is merely stopped.
func TestStoppedInitializationSlotIsStartedBeforeAnyRuntimeProbe(t *testing.T) {
	prov := &recordingProvider{state: provider.StateStopped}
	if err := probeInitializedWorkspace(context.Background(), prov, "service-1", nil); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"inspect",
		"start",
		"exec:vmbox-runtime health",
		"exec:vmbox-runtime prepare-hibernate",
	}
	if strings.Join(prov.operations, ",") != strings.Join(want, ",") {
		t.Fatalf("operations=%v want=%v", prov.operations, want)
	}
}

func TestRunningInitializationSlotIsNotRestarted(t *testing.T) {
	prov := &recordingProvider{state: provider.StateRunning}
	if err := probeInitializedWorkspace(context.Background(), prov, "service-1", nil); err != nil {
		t.Fatal(err)
	}
	for _, operation := range prov.operations {
		if operation == "start" {
			t.Fatalf("a running slot was redeployed: %v", prov.operations)
		}
	}
}

func TestInitializationSlotRecoveryFailsBeforeProbing(t *testing.T) {
	prov := &recordingProvider{state: provider.StateStopped, startErr: errors.New("no capacity")}
	err := probeInitializedWorkspace(context.Background(), prov, "service-1", nil)
	if err == nil || !strings.Contains(err.Error(), "start initialization slot") {
		t.Fatalf("err=%v", err)
	}
	if len(prov.execArgv) != 0 {
		t.Fatalf("a slot that could not start was probed anyway: %v", prov.execArgv)
	}
}

func TestInitializationStagesMatchingRuntimeBeforeHealthAndHibernate(t *testing.T) {
	prov := &recordingProvider{state: provider.StateRunning}
	runtime := []byte("current-runtime")
	if err := probeInitializedWorkspace(context.Background(), prov, "service-1", runtime); err != nil {
		t.Fatal(err)
	}
	wantPrefixes := []string{
		"inspect",
		"exec:/usr/local/bin/vmbox-runtime put-file " + stagedRuntimePath + " 0600",
		"exec:sh -c ",
		"exec:vmbox-runtime health",
		"exec:vmbox-runtime prepare-hibernate",
	}
	if len(prov.operations) != len(wantPrefixes) {
		t.Fatalf("operations=%v", prov.operations)
	}
	for index, prefix := range wantPrefixes {
		if !strings.HasPrefix(prov.operations[index], prefix) {
			t.Fatalf("operation %d=%q want prefix %q", index, prov.operations[index], prefix)
		}
	}
}

func TestCompleteLogicalBoxCreationCastsAuditProvider(t *testing.T) {
	store, mock := testStore(t)
	creation := logicalBoxCreation{
		AccountID: "account-a",
		UserID:    "user-a",
		Request:   v1.CreateLogicalBoxRequest{Provider: "railway"},
		Assignment: fleetAssignment{
			Box:          v1.LogicalBox{ID: "box-1", AssignmentGeneration: 3},
			Slot:         v1.ComputeSlot{ID: "slot-1"},
			FencingToken: "fence-1",
		},
	}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE compute_slots SET state='free'").
		WithArgs("account-a", "slot-1", int64(3), "fence-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE logical_boxes SET state='hibernated'").
		WithArgs("account-a", "box-1", int64(3), "fence-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`jsonb_build_object\('provider',\$4::text\)`).
		WithArgs("account-a", "user-a", "box-1", "railway").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := store.CompleteLogicalBoxCreation(context.Background(), creation); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseAssignmentMarksHibernateSnapshotSaved(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT slot_id::text FROM logical_boxes").
		WithArgs("account-a", "box-1", int64(4), "fence-1").
		WillReturnRows(sqlmock.NewRows([]string{"slot_id"}).AddRow("slot-1"))
	mock.ExpectExec("UPDATE compute_slots SET state='free'").
		WithArgs("account-a", "slot-1", int64(4), "fence-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE logical_boxes SET state=\\$5,restoration_state='saved'.*failure_reason=NULL").
		WithArgs("account-a", "box-1", int64(4), "fence-1", v1.LogicalBoxHibernated).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := store.ReleaseAssignment(context.Background(), "account-a", "box-1", 4, "fence-1", v1.LogicalBoxHibernated); err != nil {
		t.Fatal(err)
	}
}

func TestReusableBoxTaskSelectsOnlyLiveWork(t *testing.T) {
	tasks := func(states ...string) []v1.BoxTask {
		var values []v1.BoxTask
		for index, state := range states {
			values = append(values, v1.BoxTask{ID: string(rune('a' + index)), Agent: "claude", Session: "claude-one", State: state})
		}
		return values
	}
	tests := []struct {
		name  string
		tasks []v1.BoxTask
		state v1.LogicalBoxState
		want  string
	}{
		{name: "no tasks at all", tasks: nil, state: v1.LogicalBoxRunning},
		{name: "every task finished", tasks: tasks("completed", "failed", "cancelled"), state: v1.LogicalBoxRunning},
		{name: "active task on a running box", tasks: tasks("completed", "active"), state: v1.LogicalBoxRunning, want: "b"},
		{name: "active task on a stopped box", tasks: tasks("active"), state: v1.LogicalBoxHibernated},
		{name: "queued task on a stopped box", tasks: tasks("queued"), state: v1.LogicalBoxHibernated, want: "a"},
		{name: "newest live task wins", tasks: tasks("queued", "starting"), state: v1.LogicalBoxDetached, want: "b"},
		{name: "waiting capacity is reusable", tasks: tasks("failed", "waiting_capacity"), state: v1.LogicalBoxDetached, want: "b"},
	}
	for _, test := range tests {
		selected := reusableBoxTask(test.tasks, test.state, "claude", "")
		switch {
		case test.want == "" && selected != nil:
			t.Errorf("%s: selected %+v, wanted a new task", test.name, *selected)
		case test.want != "" && selected == nil:
			t.Errorf("%s: selected nothing, wanted task %s", test.name, test.want)
		case test.want != "" && selected.ID != test.want:
			t.Errorf("%s: selected %s, wanted %s", test.name, selected.ID, test.want)
		}
	}
}

func TestReusableBoxTaskReturnsACopy(t *testing.T) {
	tasks := []v1.BoxTask{{ID: "task-1", Agent: "claude", Session: "claude-one", State: "queued"}}
	selected := reusableBoxTask(tasks, v1.LogicalBoxRunning, "claude", "claude-one")
	if selected == nil {
		t.Fatal("a queued task was not reusable")
	}
	selected.State = "mutated"
	if tasks[0].State != "queued" {
		t.Fatalf("the selection aliased the caller slice: %+v", tasks[0])
	}
}

func TestReusableBoxTaskMatchesRequestedAgentAndSession(t *testing.T) {
	tasks := []v1.BoxTask{
		{ID: "claude-old", Agent: "claude", Session: "claude-one", State: "active"},
		{ID: "codex-new", Agent: "codex", Session: "codex-one", State: "active"},
	}
	if selected := reusableBoxTask(tasks, v1.LogicalBoxRunning, "claude", ""); selected == nil || selected.ID != "claude-old" {
		t.Fatalf("agent routing selected %+v", selected)
	}
	if selected := reusableBoxTask(tasks, v1.LogicalBoxRunning, "claude", "other-session"); selected != nil {
		t.Fatalf("session routing selected %+v", selected)
	}
}

func TestGeneratedTaskSessionsAreSafeAndDistinct(t *testing.T) {
	first, second := generatedTaskSession("codex"), generatedTaskSession("codex")
	if first == second || !strings.HasPrefix(first, "codex-") || !validSessionName(first) || !validSessionName(second) {
		t.Fatalf("generated sessions %q %q", first, second)
	}
}

func TestRecoverStaleBoxMessagesMakesInterruptedDeliveryAmbiguous(t *testing.T) {
	store, mock := testStore(t)
	before := time.Now().UTC().Add(-2 * time.Minute)
	mock.ExpectExec("UPDATE box_messages SET state='ambiguous'").WithArgs(before).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.RecoverStaleBoxMessages(context.Background(), before); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateLogicalBoxPersistsDefaultAgentWithAudit(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "account-a", UserID: "user-a", Role: "user"}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE logical_boxes SET default_agent").
		WithArgs("account-a", "box-1", "user-a", "user", "codex").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").
		WillReturnRows(logicalBoxRowWithAgent(v1.LogicalBoxRunning, "codex"))
	mock.ExpectExec("logical_box.settings.update").
		WithArgs("account-a", "user-a", "box-1", "codex").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	box, err := store.UpdateLogicalBox(context.Background(), p, "box-1", v1.UpdateLogicalBoxRequest{DefaultAgent: " CODEX "})
	if err != nil {
		t.Fatal(err)
	}
	if box.DefaultAgent != "codex" {
		t.Fatalf("default agent=%q", box.DefaultAgent)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func logicalBoxRow(state v1.LogicalBoxState) *sqlmock.Rows {
	return logicalBoxRowWithAgent(state, "claude")
}

func logicalBoxRowWithAgent(state v1.LogicalBoxState, agent string) *sqlmock.Rows {
	return logicalBoxRowWithSlot(state, agent, "slot-1")
}

func logicalBoxRowWithSlot(state v1.LogicalBoxState, agent, slotID string) *sqlmock.Rows {
	now := time.Now().UTC()
	return sqlmock.NewRows([]string{
		"id", "account_id", "owner_user_id", "name", "provider", "provider_credential",
		"default_agent", "state", "volume_id", "volume_name", "slot_id", "assignment_generation",
		"lease_owner", "lease_expires_at", "restoration_state", "failure_reason",
		"created_at", "updated_at", "tools",
	}).AddRow("box-1", "account-a", "user-a", "research", "railway", "primary", agent,
		string(state), "volume-1", "volume-name", slotID, int64(3), "", nil, "", "", now, now, "[]")
}

func TestDetachedDeletingBoxResumesFromSavedVolumeIdentity(t *testing.T) {
	store, mock := testStore(t)
	principal := Principal{AccountID: "account-a", UserID: "user-a", Role: "user"}

	mock.ExpectBegin()
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").
		WillReturnRows(logicalBoxRowWithSlot(v1.LogicalBoxDeleting, "claude", ""))
	mock.ExpectCommit()

	assignment, err := store.BeginLogicalBoxRelease(context.Background(), principal, "box-1", v1.LogicalBoxDeleting)
	if err != nil {
		t.Fatal(err)
	}
	if assignment.Released || assignment.Slot.ID != "" || assignment.Box.VolumeID != "volume-1" {
		t.Fatalf("assignment=%+v", assignment)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func boxTaskRow(id, state string) *sqlmock.Rows {
	now := time.Now().UTC()
	return sqlmock.NewRows([]string{
		"id", "logical_box_id", "name", "user_id", "requested_role", "agent",
		"session_name", "prompt", "state", "failure_reason", "created_at", "updated_at",
	}).AddRow(id, "box-1", "research", "user-a", "user", "claude", "vmbox", "hello", state, "", now, now)
}

// TestDirectMessageToATasklessBoxCreatesWorkInsteadOfPanicking pins the second
// half of 108e6a4: a box with no reusable task used to leave the selection nil
// and then dereference it, so the very first message to a fresh box crashed the
// controller instead of starting an agent.
func TestDirectMessageToATasklessBoxCreatesWorkInsteadOfPanicking(t *testing.T) {
	store, mock := testStore(t)
	now := time.Now().UTC()
	principal := Principal{AccountID: "account-a", UserID: "user-a", Role: "user"}

	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").
		WillReturnRows(logicalBoxRow(v1.LogicalBoxRunning))
	mock.ExpectQuery("FROM box_notes").WithArgs("account-a", "box-1", "key").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "body", "created_at"}))
	mock.ExpectQuery("FROM box_messages m JOIN box_tasks").
		WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "user_id", "direction", "body", "state", "created_at", "updated_at"}))
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").
		WillReturnRows(logicalBoxRow(v1.LogicalBoxRunning))
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").
		WithArgs("account-a", "box-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "logical_box_id", "name", "user_id", "requested_role", "agent",
			"session_name", "prompt", "state", "failure_reason", "created_at", "updated_at",
		}))
	mock.ExpectQuery("SELECT COALESCE\\(metadata").WithArgs("account-a", "box-1").WillReturnRows(sqlmock.NewRows([]string{"session", "agent"}).AddRow("", ""))
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").
		WillReturnRows(logicalBoxRow(v1.LogicalBoxRunning))
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").
		WithArgs("account-a", "key:task").
		WillReturnError(errNoRowsForTest)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO box_tasks").WillReturnRows(boxTaskRow("task-1", "queued"))
	mock.ExpectExec("INSERT INTO box_messages").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`jsonb_build_object\('logical_box_id',\$4::text,'agent',\$5::text\)`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").
		WithArgs("account-a", "task-1", "user-a", "user").
		WillReturnRows(boxTaskRow("task-1", "queued"))
	mock.ExpectQuery("FROM box_messages").WithArgs("account-a", "task-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "user_id", "direction", "body", "state", "created_at", "updated_at"}).
			AddRow("message-1", "task-1", "user-a", "user", "hello", "queued", now, now))
	mock.ExpectQuery("FROM box_message_images").WithArgs("account-a", "message-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "ordinal", "media_type"}))

	var started []string
	server := NewServer(store, nil)
	server.StartTask = func(_ context.Context, accountID string, task v1.BoxTask) {
		started = append(started, accountID+"/"+task.ID)
	}

	response, err := server.routeBoxMessage(context.Background(), principal, "box-1", "key",
		v1.DirectBoxMessageRequest{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Task.ID != "task-1" || response.Message.ID != "message-1" || !response.Started {
		t.Fatalf("response=%+v", response)
	}
	if len(started) != 1 || started[0] != "account-a/task-1" {
		t.Fatalf("the new task was not handed to an agent: %v", started)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// errNoRowsForTest mirrors the "no existing task for this key" answer.
var errNoRowsForTest = sql.ErrNoRows
