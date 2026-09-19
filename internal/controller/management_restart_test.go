package controller

import (
	"context"
	"reflect"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

func restartTestBoxRow() *sqlmock.Rows {
	now := time.Now().UTC()
	return sqlmock.NewRows([]string{
		"id", "account_id", "owner_user_id", "name", "provider", "provider_credential",
		"default_agent", "role", "state", "volume_id", "volume_name", "slot_id", "assignment_generation",
		"lease_owner", "lease_expires_at", "restoration_state", "failure_reason",
		"created_at", "updated_at", "tools",
	}).AddRow("box-1", "account-a", "user-a", "research", "fake", "primary", "opencode", "worker",
		"running", "volume-1", "volume-name", "", int64(3), "", nil, "", "", now, now, "[]")
}

func restartTestTaskRow(id, state string) *sqlmock.Rows {
	now := time.Now().UTC()
	return sqlmock.NewRows([]string{
		"id", "logical_box_id", "name", "user_id", "requested_role", "agent",
		"session_name", "prompt", "state", "failure_reason", "created_at", "updated_at",
	}).AddRow(id, "box-1", "research", "user-a", "user", "opencode", "opencode-one", "hello", state, "", now, now)
}

func TestDirectMessageRestartsClosedOpenCodeWithoutWaitingForReconciler(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "account-a", UserID: "user-a", Role: "user"}
	probe := &sessionProbeProvider{result: provider.ExecResult{ExitCode: 1, Stderr: "can't find session: opencode-one"}}
	server := NewServer(store, nil)
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return probe, nil }
	var started []string
	server.StartTask = func(_ context.Context, _ string, task v1.BoxTask) { started = append(started, task.ID) }

	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("FROM box_notes").WithArgs("account-a", "box-1", "key").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "body", "created_at"}))
	mock.ExpectQuery("FROM box_messages m JOIN box_tasks").WillReturnRows(emptyBoxMessageRows())
	mock.ExpectQuery("SELECT COALESCE\\(metadata").WithArgs("account-a", "box-1").WillReturnRows(sqlmock.NewRows([]string{"session", "agent"}).AddRow("opencode-one", "opencode"))
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").WithArgs("account-a", "box-1").WillReturnRows(restartTestTaskRow("old-task", "active"))
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("SELECT COALESCE\\(fencing_token").WithArgs("account-a", "box-1").WillReturnRows(sqlmock.NewRows([]string{"fencing_token"}).AddRow("fence"))
	mock.ExpectExec("UPDATE box_tasks t SET state='failed'").WithArgs("account-a", "old-task", "", int64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").WithArgs("account-a", "box-1").WillReturnRows(restartTestTaskRow("old-task", "failed"))
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").WithArgs("account-a", "key:task").WillReturnError(errNoRowsForTest)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO box_tasks").WillReturnRows(restartTestTaskRow("new-task", "queued"))
	mock.ExpectExec("INSERT INTO box_messages").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("jsonb_build_object").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").WithArgs("account-a", "new-task", "user-a", "user").WillReturnRows(restartTestTaskRow("new-task", "queued"))
	mock.ExpectQuery("FROM box_messages").WithArgs("account-a", "new-task").WillReturnRows(boxMessageRow("message-1", "new-task", "user-a", "user", "hello", "queued"))
	mock.ExpectQuery("FROM box_message_images").WithArgs("account-a", "message-1").WillReturnRows(sqlmock.NewRows([]string{"id", "ordinal", "media_type"}))

	response, err := server.routeBoxMessage(context.Background(), p, "box-1", "key", v1.DirectBoxMessageRequest{Text: "hello"})
	if err != nil || !response.Started || response.Task.ID != "new-task" || response.Message.TaskID != "new-task" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	if !reflect.DeepEqual(started, []string{"new-task"}) || !reflect.DeepEqual(probe.argv, []string{"tmux", "has-session", "-t", "=opencode-one"}) {
		t.Fatalf("started=%v probe=%v", started, probe.argv)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDirectMessageDoesNotRestartWhenSessionProbeIsUncertain(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "account-a", UserID: "user-a", Role: "user"}
	probe := &sessionProbeProvider{result: provider.ExecResult{ExitCode: 1, Stderr: "error connecting to tmux socket (Permission denied)"}}
	server := NewServer(store, nil)
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return probe, nil }
	server.StartTask = func(_ context.Context, _ string, task v1.BoxTask) {
		t.Fatalf("uncertain probe started task %s", task.ID)
	}

	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("FROM box_notes").WithArgs("account-a", "box-1", "key").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "body", "created_at"}))
	mock.ExpectQuery("FROM box_messages m JOIN box_tasks").WillReturnRows(emptyBoxMessageRows())
	mock.ExpectQuery("SELECT COALESCE\\(metadata").WithArgs("account-a", "box-1").WillReturnRows(sqlmock.NewRows([]string{"session", "agent"}).AddRow("opencode-one", "opencode"))
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").WithArgs("account-a", "box-1").WillReturnRows(restartTestTaskRow("old-task", "active"))
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("SELECT COALESCE\\(fencing_token").WithArgs("account-a", "box-1").WillReturnRows(sqlmock.NewRows([]string{"fencing_token"}).AddRow("fence"))

	if _, err := server.routeBoxMessage(context.Background(), p, "box-1", "key", v1.DirectBoxMessageRequest{Text: "hello"}); err == nil {
		t.Fatal("uncertain probe must not file the message under a dead task or start a second harness")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
