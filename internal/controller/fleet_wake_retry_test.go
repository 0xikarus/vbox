package controller

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestSharedHostOverloaded(t *testing.T) {
	for _, test := range []struct {
		cores, load float64
		want        bool
	}{
		{3, 100, true},
		{3, 11, false},
		{3, 12, true},
		{1, 7, false},
		{1, 8, true},
		{0, 100, false},
	} {
		got := sharedHostOverloaded(provider.HostResources{CPUCores: test.cores, CPULoad1: test.load})
		if got != test.want {
			t.Errorf("cores=%v load=%v: got %v, want %v", test.cores, test.load, got, test.want)
		}
	}
}

func timedOutBoxRow(volume string) *sqlmock.Rows {
	now := time.Now().UTC()
	return sqlmock.NewRows([]string{
		"id", "account_id", "owner_user_id", "name", "provider", "provider_credential", "default_agent", "roles", "state", "volume_id", "volume_name", "slot_id", "assignment_generation", "lease_owner", "lease_expires_at", "restoration_state", "failure_reason", "created_at", "updated_at", "tools",
	}).AddRow("box-1", "account-a", "user-a", "research", "shared-worker", "primary", "claude", "[]", "failed", volume, "volume-name", "slot-1", int64(3), "", nil, "attach-timed-out", "attachment timed out", now, now, "[]")
}

func TestRetryTimedOutAllocationRetainsFenceAndVolume(t *testing.T) {
	store, mock := testStore(t)
	now := time.Now().UTC()
	assignment := fleetAssignment{Box: v1.LogicalBox{ID: "box-1", VolumeID: "volume-1", AssignmentGeneration: 3}, Slot: v1.ComputeSlot{ID: "slot-1", ServiceID: "service-1"}, FencingToken: "fence-1"}
	p := Principal{AccountID: "account-a", UserID: "user-a", Role: "user"}
	allocationColumns := []string{"id", "idempotency_key", "state", "logical_box_id", "logical_box_name", "slot_id", "service_id", "assignment_generation", "fencing_token", "lease_owner", "lease_expires_at", "phase", "retry_count", "failure_reason", "created_at", "updated_at"}
	mock.ExpectBegin()
	mock.ExpectQuery("FROM logical_boxes.*FOR UPDATE").WithArgs("account-a", "box-1").WillReturnRows(timedOutBoxRow("volume-1"))
	mock.ExpectQuery("FROM compute_slots.*FOR UPDATE OF s").WithArgs("account-a", "slot-1").WillReturnRows(sqlmock.NewRows(computeSlotColumns()).AddRow("slot-1", "account-a", "shared-worker", "primary", 1, "draining", "service-1", "worker-1", "", "box-1", "research", "", "", "", "healthy", int64(3), "", nil, "attachment timed out", now, now))
	mock.ExpectQuery("FROM allocation_requests.*FOR UPDATE OF r").WithArgs("account-a", "box-1", int64(3), "fence-1").WillReturnRows(sqlmock.NewRows(allocationColumns).AddRow("allocation-1", "original-key", "failed", "box-1", "research", "slot-1", "service-1", int64(3), "fence-1", "", nil, "attach-timed-out", 0, "attachment timed out", now, now))
	mock.ExpectQuery("SELECT EXISTS").WithArgs("account-a", "box-1", "allocation-1").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec("UPDATE logical_boxes SET state='reserved'").WithArgs("account-a", "box-1", "slot-1", int64(3), "user:user-a", sqlmock.AnyArg(), "fence-1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE compute_slots SET state='reserved'").WithArgs("account-a", "slot-1", int64(3), "fence-1", "user:user-a", sqlmock.AnyArg(), "").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE allocation_requests SET state='reserved'").WithArgs("account-a", "allocation-1", "slot-1", int64(3), "fence-1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO audit_log").WithArgs("account-a", "user-a", "box-1", "allocation-1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("FROM allocation_requests.*r.id=\\$2").WithArgs("account-a", "allocation-1").WillReturnRows(sqlmock.NewRows(allocationColumns).AddRow("allocation-1", "original-key", "reserved", "box-1", "research", "slot-1", "service-1", int64(3), "fence-1", "user:user-a", now.Add(time.Minute), "reserved", 0, "", now, now))
	allocation, err := store.RetryTimedOutAllocation(context.Background(), p, assignment, "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if allocation.State != "reserved" || allocation.FencingToken != "fence-1" || allocation.RequestID != "allocation-1" {
		t.Fatalf("retry changed allocation identity: %+v", allocation)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRetryTimedOutAllocationRejectsPlaceholderVolume(t *testing.T) {
	store, mock := testStore(t)
	assignment := fleetAssignment{Box: v1.LogicalBox{ID: "box-1", VolumeID: "pending:box", AssignmentGeneration: 3}, Slot: v1.ComputeSlot{ID: "slot-1"}, FencingToken: "fence-1"}
	mock.ExpectBegin()
	mock.ExpectQuery("FROM logical_boxes.*FOR UPDATE").WithArgs("account-a", "box-1").WillReturnRows(timedOutBoxRow("pending:box"))
	mock.ExpectRollback()
	_, err := store.RetryTimedOutAllocation(context.Background(), Principal{AccountID: "account-a", UserID: "user-a"}, assignment, "", 0)
	if err == nil || !strings.Contains(err.Error(), "no longer eligible") {
		t.Fatalf("placeholder retry error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTimedOutWakeRetryPostgres(t *testing.T) {
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
	ns := "wake_retry_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err := store.DB.ExecContext(ctx, "CREATE SCHEMA "+ns); err != nil {
		t.Fatal(err)
	}
	defer store.DB.ExecContext(context.Background(), "DROP SCHEMA "+ns+" CASCADE")
	if _, err := store.DB.ExecContext(ctx, "SET search_path TO "+ns); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := store.Bootstrap(ctx, "wake retry test", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	boxID, slotID, requestID := uuid(), uuid(), uuid()
	const fence = "retry-test-fence"
	const volumeID = "retry-test-volume"
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,ordinal,state,service_id,health,assignment_generation,fencing_token) VALUES($1,$2,'shared-worker',1,'draining','test-service','healthy',3,$3)`, slotID, p.AccountID, fence); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,slot_id,assignment_generation,fencing_token,restoration_state) VALUES($1,$2,$3,'retry-test','shared-worker','failed',$4,'retry-test-volume',$5,3,$6,'attach-timed-out')`, boxID, p.AccountID, p.UserID, volumeID, slotID, fence); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO allocation_requests(id,account_id,logical_box_id,state,idempotency_key,requested_by,slot_id,assignment_generation,fencing_token,phase,attach_started_at) VALUES($1,$2,$3,'failed','wake-retry-test',$4,$5,3,$6,'attach-timed-out',now()-interval '31 minutes')`, requestID, p.AccountID, boxID, p.UserID, slotID, fence); err != nil {
		t.Fatal(err)
	}
	assignment, err := store.assignment(ctx, p.AccountID, boxID)
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := store.RetryTimedOutAllocation(ctx, p, assignment, "retry-test", 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if allocation.RequestID != requestID || allocation.FencingToken != fence || allocation.State != "reserved" {
		t.Fatalf("retry changed allocation identity: %+v", allocation)
	}
	var retainedVolume, boxState, slotState string
	if err := store.DB.QueryRowContext(ctx, `SELECT b.volume_id,b.state,s.state FROM logical_boxes b JOIN compute_slots s ON s.id=b.slot_id WHERE b.id=$1`, boxID).Scan(&retainedVolume, &boxState, &slotState); err != nil {
		t.Fatal(err)
	}
	if retainedVolume != volumeID || boxState != "reserved" || slotState != "reserved" {
		t.Fatalf("volume=%s box=%s slot=%s", retainedVolume, boxState, slotState)
	}
	if _, err := store.DB.ExecContext(ctx, `UPDATE allocation_requests SET state='attaching',phase='waiting-for-host-load',attach_started_at=now()-interval '31 minutes' WHERE id=$1`, requestID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='attaching' WHERE id=$1`, boxID); err != nil {
		t.Fatal(err)
	}
	count, err := store.FailTimedOutAttaches(ctx)
	if err != nil || count != 0 {
		t.Fatalf("waiting allocation timed out: count=%d err=%v", count, err)
	}
	if err := store.MarkAssignmentAttaching(ctx, allocation); err != nil {
		t.Fatal(err)
	}
	var started time.Time
	if err := store.DB.QueryRowContext(ctx, `SELECT attach_started_at FROM allocation_requests WHERE id=$1`, requestID).Scan(&started); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Minute {
		t.Fatalf("retry retained the old attach timeout: %s", started)
	}
}
