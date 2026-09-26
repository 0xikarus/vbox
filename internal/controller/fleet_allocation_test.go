package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestReserveAllocationFollowsMatchingInProgressRequest(t *testing.T) {
	store, mock := testStore(t)
	now := time.Now().UTC()
	principal := Principal{AccountID: "account-a", UserID: "user-a", Role: "user"}

	mock.ExpectQuery("FROM allocation_requests.*r.idempotency_key=\\$2").
		WithArgs("account-a", "second-key").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "idempotency_key", "state", "logical_box_id", "logical_box_name",
			"slot_id", "service_id", "assignment_generation", "fencing_token",
			"lease_owner", "lease_expires_at", "phase", "retry_count", "failure_reason",
			"created_at", "updated_at",
		}))
	mock.ExpectBegin()
	mock.ExpectQuery("FROM logical_boxes.*FOR UPDATE").
		WithArgs("account-a", "research").
		WillReturnRows(logicalBoxRow(v1.LogicalBoxAttaching))
	mock.ExpectQuery("FROM allocation_requests.*r.logical_box_id=\\$2.*r.assignment_generation=\\$3").
		WithArgs("account-a", "box-1", int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "idempotency_key", "state", "logical_box_id", "logical_box_name",
			"slot_id", "service_id", "assignment_generation", "fencing_token",
			"lease_owner", "lease_expires_at", "phase", "retry_count", "failure_reason",
			"created_at", "updated_at",
		}).AddRow("allocation-1", "original-key", "attaching", "box-1", "research",
			"slot-1", "service-1", int64(3), "fence-1", "cli", now.Add(time.Minute),
			"attaching-volume", 0, "", now, now))
	mock.ExpectCommit()

	allocation, err := store.ReserveAllocation(context.Background(), principal, "research", "second-key", "cli", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if allocation.RequestID != "allocation-1" || allocation.IdempotencyKey != "original-key" || allocation.State != "attaching" {
		t.Fatalf("allocation=%+v", allocation)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSelectedWorkerIsNotReplacedDuringInitialStart(t *testing.T) {
	store, mock := testStore(t)
	principal := Principal{AccountID: "account-a", UserID: "user-a", Role: "user"}
	mock.ExpectQuery("FROM allocation_requests.*r.idempotency_key=\\$2").WithArgs("account-a", "create-key").
		WillReturnRows(sqlmock.NewRows([]string{"id", "idempotency_key", "state", "logical_box_id", "logical_box_name", "slot_id", "service_id", "assignment_generation", "fencing_token", "lease_owner", "lease_expires_at", "phase", "retry_count", "failure_reason", "created_at", "updated_at"}))
	mock.ExpectBegin()
	mock.ExpectQuery("FROM logical_boxes.*FOR UPDATE").WithArgs("account-a", "research").
		WillReturnRows(logicalBoxRowWithSlot(v1.LogicalBoxHibernated, "codex", ""))
	mock.ExpectQuery("FROM allocation_requests.*r.state='queued'").WithArgs("account-a", "box-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "idempotency_key", "state", "logical_box_id", "logical_box_name", "slot_id", "service_id", "assignment_generation", "fencing_token", "lease_owner", "lease_expires_at", "phase", "retry_count", "failure_reason", "created_at", "updated_at"}))
	mock.ExpectQuery(`FROM compute_slots.*s.id::text=\$5.*FOR UPDATE OF s SKIP LOCKED LIMIT 1`).WithArgs("account-a", "railway", "primary", "box-1", "slot-2").
		WillReturnRows(sqlmock.NewRows(computeSlotColumns()))
	mock.ExpectRollback()
	_, err := store.ReserveAllocationOnSlot(context.Background(), principal, "research", "create-key", "creator", time.Minute, "slot-2")
	if err == nil || !strings.Contains(err.Error(), "selected worker slot is no longer available") {
		t.Fatalf("error=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAllocationProgressCannotOverwriteTerminalState(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec("UPDATE allocation_requests SET phase=.*state IN \\('queued','reserved','attaching'\\)").
		WithArgs("account-a", "allocation-1", "marking-ready", "stale assignment fencing token", 1).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := store.UpdateAllocationProgress(context.Background(), "account-a", "allocation-1", "marking-ready", "stale assignment fencing token", true); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
