package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func queueFollowupCapabilityRows(t *testing.T, mock sqlmock.Sqlmock, account, box string, maxPending int) {
	t.Helper()
	config, err := json.Marshal(v1.QueueFollowupGrant{Enabled: true, MaxDelayMinutes: 60, MaxPending: maxPending})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("FROM box_role_assignments").WithArgs(account, box).
		WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionQueueFollowup, config))
}

func TestQueueAgentFollowupLocksQuotaAndBindsExactInteractiveTask(t *testing.T) {
	store, mock := testStore(t)
	queueFollowupCapabilityRows(t, mock, "account-a", "box-a", 1)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT assignment_generation FROM logical_boxes.*FOR UPDATE").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"assignment_generation"}).AddRow(int64(7)))
	mock.ExpectQuery("SELECT id::text,text,delay_seconds").WithArgs("account-a", "box-a", "followup-key").
		WillReturnRows(sqlmock.NewRows([]string{"id", "text", "delay_seconds", "due_at", "state", "created_at"}))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM agent_followups").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT id::text FROM box_tasks").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("task-a"))
	mock.ExpectQuery("INSERT INTO agent_followups").WithArgs(sqlmock.AnyArg(), "account-a", "box-a", "task-a", "user-a", int64(7), "check status", 30, sqlmock.AnyArg(), "followup-key").
		WillReturnRows(sqlmock.NewRows([]string{"id", "text", "due_at", "state", "created_at"}).AddRow("followup-a", "check status", time.Now(), "queued", time.Now()))
	mock.ExpectCommit()

	value, reused, err := store.QueueAgentFollowup(context.Background(), Principal{AccountID: "account-a", UserID: "user-a"}, "box-a", "followup-key", v1.QueueFollowupRequest{Text: "check status", DelaySeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	if reused || value.ID != "followup-a" {
		t.Fatalf("follow-up=%+v reused=%v", value, reused)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestQueueAgentFollowupRejectsAmbiguousInteractiveTasks(t *testing.T) {
	store, mock := testStore(t)
	queueFollowupCapabilityRows(t, mock, "account-a", "box-a", 2)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT assignment_generation FROM logical_boxes.*FOR UPDATE").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"assignment_generation"}).AddRow(int64(7)))
	mock.ExpectQuery("SELECT id::text,text,delay_seconds").WithArgs("account-a", "box-a", "followup-key").
		WillReturnRows(sqlmock.NewRows([]string{"id", "text", "delay_seconds", "due_at", "state", "created_at"}))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM agent_followups").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT id::text FROM box_tasks").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("task-a").AddRow("task-b"))
	mock.ExpectRollback()

	_, _, err := store.QueueAgentFollowup(context.Background(), Principal{AccountID: "account-a", UserID: "user-a"}, "box-a", "followup-key", v1.QueueFollowupRequest{Text: "check status"})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("ambiguous task error=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileAgentFollowupDeliversOnlyToBoundTask(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("SELECT f.account_id::text").WillReturnRows(sqlmock.NewRows([]string{"account_id", "id", "box_id", "created_by", "text", "task_id", "task_state", "agent"}).
		AddRow("account-a", "followup-a", "box-a", "user-a", "bound message", "task-original", "active", "codex"))
	queueFollowupCapabilityRows(t, mock, "account-a", "box-a", 1)
	mock.ExpectExec("UPDATE agent_followups SET state='delivering'").WithArgs("account-a", "followup-a").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO box_messages").WithArgs(sqlmock.AnyArg(), "account-a", "task-original", "user-a", "bound message", "agent-followup:followup-a", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE agent_followups SET state='delivered'").WithArgs("account-a", "followup-a").WillReturnResult(sqlmock.NewResult(0, 1))

	server := &Server{Store: store}
	if err := server.ReconcileAgentFollowupsNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
