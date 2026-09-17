package controller

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCreateBoxTaskRetriesConcurrentSerializableUpdate(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "account", UserID: "user", Role: "owner"}
	box := v1.LogicalBox{ID: "box", Name: "test-box"}
	request := v1.CreateBoxTaskRequest{Agent: "codex", Session: "codex-test", Prompt: "test prompt"}
	insertArgs := []driver.Value{sqlmock.AnyArg(), "account", "box", "user", "owner", "codex", "codex-test", "test prompt", "message-key:task", "test-box"}

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO box_tasks`).WithArgs(insertArgs...).WillReturnError(&pgconn.PgError{Code: "40001", Message: "could not serialize access due to concurrent update"})
	mock.ExpectRollback()

	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO box_tasks`).WithArgs(insertArgs...).WillReturnRows(sqlmock.NewRows([]string{
		"id", "logical_box_id", "box_name", "user_id", "requested_role", "agent", "session_name", "prompt", "state", "failure_reason", "created_at", "updated_at",
	}).AddRow("task", "box", "test-box", "user", "owner", "codex", "codex-test", "test prompt", "queued", "", now, now))
	mock.ExpectExec(`INSERT INTO box_messages`).WithArgs(sqlmock.AnyArg(), "account", "task", "user", "test prompt", "message-key:task:initial", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO audit_log`).WithArgs("account", "user", "task", "box", "codex").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	task, reused, err := store.createBoxTaskWithRetry(context.Background(), p, box, "message-key:task", request)
	if err != nil || reused || task.ID != "task" {
		t.Fatalf("task=%+v reused=%v err=%v", task, reused, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
