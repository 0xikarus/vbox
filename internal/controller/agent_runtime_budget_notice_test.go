package controller

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRunBudgetStopDecisionFixedClock(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(-time.Minute)
	busy := sql.NullBool{Bool: true, Valid: true}
	recent := sql.NullTime{Time: now.Add(-5 * time.Second), Valid: true}
	for _, test := range []struct {
		name     string
		at       time.Time
		deadline time.Time
		busy     sql.NullBool
		observed sql.NullTime
		want     string
	}{
		{"before expiry", now, now.Add(time.Minute), busy, recent, ""},
		{"expiring busy defers", now, deadline, busy, recent, ""},
		{"idle stops", now, deadline, sql.NullBool{Valid: true}, recent, "run-limit"},
		{"stale liveness stops", now, deadline, busy, sql.NullTime{Time: now.Add(-time.Minute), Valid: true}, "run-limit"},
		{"quiet activity stops", now, deadline, busy, recent, "run-limit"},
		{"hard cap stops busy", now, now.Add(-runBudgetHardCap), busy, recent, "run-limit-hard-cap"},
	} {
		t.Run(test.name, func(t *testing.T) {
			evidence := test.observed
			busySince := recent
			if test.name == "quiet activity stops" {
				evidence = sql.NullTime{Time: now.Add(-2 * time.Minute), Valid: true}
				busySince = evidence
			}
			got := runBudgetStopReason(test.at, test.deadline, test.busy, busySince, test.observed, evidence, sql.NullTime{}, "", "working")
			if got != test.want {
				t.Fatalf("stop reason = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRunBudgetNoticeFixedClock(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	if runBudgetNoticeDue(now, now.Add(11*time.Minute), false) {
		t.Fatal("notice sent too early")
	}
	if !runBudgetNoticeDue(now, now.Add(10*time.Minute), false) {
		t.Fatal("notice missing at ten minutes")
	}
	if runBudgetNoticeDue(now, now.Add(10*time.Minute), true) {
		t.Fatal("notice repeated")
	}
	if runBudgetNoticeDue(now, now, false) {
		t.Fatal("notice sent after expiry")
	}
}

func TestLogicalBoxStopCause(t *testing.T) {
	for _, test := range []struct{ subject, checked, want string }{
		{"controller:run-budget", "run-limit", "run-limit"},
		{"controller:run-budget", "run-limit-hard-cap", "run-limit-hard-cap"},
		{"controller:desktop-idle", "", "idle"},
		{"controller:capacity", "", "capacity"},
		{"owner", "", "manual"},
	} {
		if got := logicalBoxStopReason(test.subject, test.checked); got != test.want {
			t.Errorf("subject %q: got %q, want %q", test.subject, got, test.want)
		}
	}
}

func TestRunBudgetNoticeClaimedOnce(t *testing.T) {
	store, mock := testStore(t)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	pushes := 0
	server := &Server{Store: store, SendRunBudgetPush: func(accountID, boxID string) {
		if accountID != "account-a" || boxID != "box-a" {
			t.Fatalf("wrong push target %s/%s", accountID, boxID)
		}
		pushes++
	}}
	mock.ExpectBegin()
	mock.ExpectQuery("UPDATE agent_run_budgets rb SET notice_sent_at").WithArgs("account-a", "box-a", now).
		WillReturnRows(sqlmock.NewRows([]string{"assignment_generation"}).AddRow(3))
	mock.ExpectQuery("SELECT id::text,state FROM box_tasks").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"id", "state"}).AddRow("task-a", "active"))
	mock.ExpectExec("INSERT INTO box_messages").WithArgs(sqlmock.AnyArg(), "account-a", "task-a", sqlmock.AnyArg(), true, "queued", "run-budget-notice:box-a:3", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := server.sendRunBudgetNotice(context.Background(), "account-a", "box-a", now); err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("UPDATE agent_run_budgets rb SET notice_sent_at").WithArgs("account-a", "box-a", now).
		WillReturnRows(sqlmock.NewRows([]string{"assignment_generation"}))
	mock.ExpectRollback()
	if err := server.sendRunBudgetNotice(context.Background(), "account-a", "box-a", now); err != nil {
		t.Fatal(err)
	}
	if pushes != 1 {
		t.Fatalf("owner pushes = %d, want one", pushes)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
