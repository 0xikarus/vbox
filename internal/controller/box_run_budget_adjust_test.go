package controller

import (
	"context"
	"database/sql/driver"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

type secondsNear struct{ want int64 }

func (match secondsNear) Match(value driver.Value) bool {
	seconds, ok := value.(int64)
	return ok && seconds >= match.want-2 && seconds <= match.want+2
}

type deadlineNear struct{ want time.Time }

func (match deadlineNear) Match(value driver.Value) bool {
	deadline, ok := value.(time.Time)
	return ok && deadline.Sub(match.want) < 2*time.Second && match.want.Sub(deadline) < 2*time.Second
}

func expectSyncedRunBudget(mock sqlmock.Sqlmock, deadline time.Time) {
	mock.ExpectQuery("SELECT state,assignment_generation,").WithArgs("account-a", "box-a", int64(8*3600), maxAgentRunBudgetSeconds).
		WillReturnRows(sqlmock.NewRows([]string{"state", "generation", "seconds"}).AddRow("running", 7, 8*3600))
	mock.ExpectExec("INSERT INTO agent_run_budgets").WithArgs("account-a", "box-a", int64(7), int64(8*3600), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE agent_run_budgets SET deadline_at=now").WithArgs("account-a", "box-a").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT remaining_seconds,deadline_at,extension_seconds").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"remaining_seconds", "deadline_at", "extension_seconds"}).AddRow(2*3600, deadline, 0))
}

func TestAdjustBoxRunBudgetChangesOnlyCurrentCountdown(t *testing.T) {
	for _, test := range []struct {
		name, action string
		addSeconds   int64
		wantSeconds  int64
		reset        bool
	}{
		{"add two hours", "add", 2 * 3600, 4 * 3600, false},
		{"add", "add", 4 * 3600, 6 * 3600, false},
		{"reset", "reset", 0, 8 * 3600, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, mock := testStore(t)
			deadline := time.Now().UTC().Add(2 * time.Hour)
			expectSyncedRunBudget(mock, deadline)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT state,assignment_generation,").WithArgs("account-a", "box-a", int64(8*3600), maxAgentRunBudgetSeconds).
				WillReturnRows(sqlmock.NewRows([]string{"state", "generation", "seconds"}).AddRow("running", 7, 8*3600))
			mock.ExpectQuery("SELECT deadline_at FROM agent_run_budgets").WithArgs("account-a", "box-a", int64(7)).
				WillReturnRows(sqlmock.NewRows([]string{"deadline_at"}).AddRow(deadline))
			mock.ExpectExec("UPDATE agent_run_budgets SET remaining_seconds=").
				WithArgs("account-a", "box-a", int64(7), secondsNear{test.wantSeconds}, deadlineNear{time.Now().UTC().Add(time.Duration(test.wantSeconds) * time.Second)}, test.reset).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			if err := store.AdjustBoxRunBudget(context.Background(), "account-a", "box-a", test.action, test.addSeconds, deadline, 8*time.Hour); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunBudgetPolicyHandlerAcceptsNoticeTwoHours(t *testing.T) {
	store, mock := testStore(t)
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	principal := Principal{AccountID: "a", UserID: "u", Role: "owner"}
	mock.ExpectQuery("FROM logical_boxes WHERE account_id=").WithArgs("a", "box-a").
		WillReturnRows(logicalBoxRows("box-a", "running"))
	expectSync := func(next time.Time, remaining int64) {
		mock.ExpectQuery("SELECT state,assignment_generation,").WithArgs("a", "box-a", int64(8*3600), maxAgentRunBudgetSeconds).
			WillReturnRows(sqlmock.NewRows([]string{"state", "generation", "seconds"}).AddRow("running", 1, 8*3600))
		mock.ExpectExec("INSERT INTO agent_run_budgets").WithArgs("a", "box-a", int64(1), int64(8*3600), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("UPDATE agent_run_budgets SET deadline_at=now").WithArgs("a", "box-a").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery("SELECT remaining_seconds,deadline_at,extension_seconds").WithArgs("a", "box-a").
			WillReturnRows(sqlmock.NewRows([]string{"remaining_seconds", "deadline_at", "extension_seconds"}).AddRow(remaining, next, 0))
	}
	expectSync(deadline, 3600)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state,assignment_generation,").WithArgs("a", "box-a", int64(8*3600), maxAgentRunBudgetSeconds).
		WillReturnRows(sqlmock.NewRows([]string{"state", "generation", "seconds"}).AddRow("running", 1, 8*3600))
	mock.ExpectQuery("SELECT deadline_at FROM agent_run_budgets").WithArgs("a", "box-a", int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"deadline_at"}).AddRow(deadline))
	mock.ExpectExec("UPDATE agent_run_budgets SET remaining_seconds=").
		WithArgs("a", "box-a", int64(1), secondsNear{3 * 3600}, deadlineNear{deadline.Add(2 * time.Hour)}, false).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	expectSync(deadline.Add(2*time.Hour), 3*3600)
	mock.ExpectQuery(`SELECT MAX\(updated_at\) FROM allocation_requests`).WithArgs("a", "box-a", int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"started"}).AddRow(nil))
	request := httptest.NewRequest(http.MethodPost, "/v1/logical-boxes/box-a/run-budget-policy/adjust",
		strings.NewReader(fmt.Sprintf(`{"action":"add","seconds":7200,"expectedDeadlineAt":%q}`, deadline.Format(time.RFC3339Nano))))
	request.SetPathValue("id", "box-a")
	response := httptest.NewRecorder()
	(&Server{Store: store, DefaultRunBudget: 8 * time.Hour}).boxRunBudgetPolicy(response, request, principal)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"remainingSeconds"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAdjustBoxRunBudgetRejectsStaleDeadline(t *testing.T) {
	store, mock := testStore(t)
	deadline := time.Now().UTC().Add(2 * time.Hour)
	expectSyncedRunBudget(mock, deadline)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state,assignment_generation,").WithArgs("account-a", "box-a", int64(8*3600), maxAgentRunBudgetSeconds).
		WillReturnRows(sqlmock.NewRows([]string{"state", "generation", "seconds"}).AddRow("running", 7, 8*3600))
	mock.ExpectQuery("SELECT deadline_at FROM agent_run_budgets").WithArgs("account-a", "box-a", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"deadline_at"}).AddRow(deadline))
	mock.ExpectRollback()
	if err := store.AdjustBoxRunBudget(context.Background(), "account-a", "box-a", "add", 4*3600, deadline.Add(-time.Minute), 8*time.Hour); err == nil || !strings.Contains(err.Error(), "countdown changed") {
		t.Fatalf("stale adjustment accepted: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
