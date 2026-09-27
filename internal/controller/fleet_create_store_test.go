package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func computeSlotColumns() []string {
	return []string{"id", "account_id", "provider", "provider_credential", "ordinal", "state", "service_id", "service_name", "deployment_instance_id", "logical_box_id", "logical_box_name", "region", "image", "image_version", "health", "assignment_generation", "lease_owner", "lease_expires_at", "failure_reason", "created_at", "updated_at"}
}

func TestBeginLogicalBoxCreationFencesExactlyOneFreeSlot(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{DB: db, agentCLIPackageVersionCheck: func(_ context.Context, agent, version string) error {
		if agent != "claude" || version != "2.1.280" {
			t.Fatalf("unexpected agent CLI lookup: %s %s", agent, version)
		}
		return nil
	}}
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT claude_version,codex_version,opencode_version FROM agent_cli_versions").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"claude_version", "codex_version", "opencode_version"}).AddRow("2.1.280", "", ""))
	mock.ExpectQuery("SELECT id::text FROM logical_boxes").WithArgs("account-a", "research").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("FROM compute_slots.*FOR UPDATE OF s SKIP LOCKED LIMIT 1").WithArgs("account-a", "railway", "primary").WillReturnRows(sqlmock.NewRows(computeSlotColumns()).AddRow("slot-1", "account-a", "railway", "primary", 1, "free", "service-1", "slot-a-01", "deployment-1", "", "", "ams", "image@sha256:digest", "v1", "healthy", int64(7), "", nil, "", now, now))
	mock.ExpectExec("UPDATE compute_slots SET state='reserved'").WithArgs("account-a", "slot-1", int64(8), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO logical_boxes").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	creation, err := store.BeginLogicalBoxCreation(context.Background(), Principal{AccountID: "account-a", UserID: "user-a"}, v1.CreateLogicalBoxRequest{Name: "research", Provider: "railway", ProviderCredential: "primary", DiskGiB: 20})
	if err != nil {
		t.Fatal(err)
	}
	if creation.Assignment.Slot.ID != "slot-1" || creation.Assignment.Box.SlotID != "slot-1" || creation.Assignment.Box.AssignmentGeneration != 8 {
		t.Fatalf("creation=%+v", creation)
	}
	if creation.AgentCLIVersion != "2.1.280" {
		t.Fatalf("agent CLI version snapshot=%q", creation.AgentCLIVersion)
	}
	if !pendingVolume(creation.Assignment.Box.VolumeID) || creation.Assignment.FencingToken == "" {
		t.Fatalf("creation was not fenced with a non-adoptable placeholder: %+v", creation)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBeginLogicalBoxCreationRejectsUnpublishedCLIBeforeSlotReservation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{DB: db, agentCLIPackageVersionCheck: func(_ context.Context, agent, version string) error {
		if agent != "claude" || version != "999.999.999" {
			t.Fatalf("unexpected agent CLI lookup: %s %s", agent, version)
		}
		return errors.New("version is not published")
	}}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT claude_version,codex_version,opencode_version FROM agent_cli_versions").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"claude_version", "codex_version", "opencode_version"}).AddRow("999.999.999", "", ""))
	mock.ExpectRollback()
	_, err = store.BeginLogicalBoxCreation(context.Background(), Principal{AccountID: "account-a", UserID: "user-a"}, v1.CreateLogicalBoxRequest{Name: "research", Provider: "railway", ProviderCredential: "primary", DiskGiB: 20})
	if err == nil || !strings.Contains(err.Error(), "not published") {
		t.Fatalf("unpublished agent CLI was accepted: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBeginLogicalBoxCreationReservesSelectedWorkerSlot(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{DB: db}
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT claude_version,codex_version,opencode_version FROM agent_cli_versions").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"claude_version", "codex_version", "opencode_version"}))
	mock.ExpectQuery("SELECT id::text FROM logical_boxes").WithArgs("account-a", "research").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`FROM compute_slots.*s.id::text=\$4.*FOR UPDATE OF s SKIP LOCKED LIMIT 1`).WithArgs("account-a", "railway", "primary", "slot-2").WillReturnRows(sqlmock.NewRows(computeSlotColumns()).AddRow("slot-2", "account-a", "railway", "primary", 2, "free", "service-2", "worker-two", "deployment-2", "", "", "ams", "image@sha256:digest", "v1", "healthy", int64(7), "", nil, "", now, now))
	mock.ExpectExec("UPDATE compute_slots SET state='reserved'").WithArgs("account-a", "slot-2", int64(8), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO logical_boxes").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	creation, err := store.BeginLogicalBoxCreation(context.Background(), Principal{AccountID: "account-a", UserID: "user-a"}, v1.CreateLogicalBoxRequest{Name: "research", Provider: "railway", ProviderCredential: "primary", DiskGiB: 20, SlotID: "slot-2"})
	if err != nil || creation.Assignment.Slot.ID != "slot-2" {
		t.Fatalf("creation slot=%q error=%v", creation.Assignment.Slot.ID, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBeginLogicalBoxCreationDoesNotFallBackFromSelectedWorker(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{DB: db}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT claude_version,codex_version,opencode_version FROM agent_cli_versions").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"claude_version", "codex_version", "opencode_version"}))
	mock.ExpectQuery("SELECT id::text FROM logical_boxes").WithArgs("account-a", "research").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`FROM compute_slots.*s.id::text=\$4.*FOR UPDATE OF s SKIP LOCKED LIMIT 1`).WithArgs("account-a", "railway", "primary", "slot-2").WillReturnRows(sqlmock.NewRows(computeSlotColumns()))
	mock.ExpectRollback()
	_, err = store.BeginLogicalBoxCreation(context.Background(), Principal{AccountID: "account-a", UserID: "user-a"}, v1.CreateLogicalBoxRequest{Name: "research", Provider: "railway", ProviderCredential: "primary", DiskGiB: 20, SlotID: "slot-2"})
	if err == nil || !strings.Contains(err.Error(), "selected worker slot is no longer available") {
		t.Fatalf("error=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBeginLogicalBoxCreationQueuesNoUnmanagedServiceWhenFleetIsFull(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{DB: db}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT claude_version,codex_version,opencode_version FROM agent_cli_versions").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"claude_version", "codex_version", "opencode_version"}))
	mock.ExpectQuery("SELECT id::text FROM logical_boxes").WithArgs("account-a", "research").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("FROM compute_slots.*FOR UPDATE OF s SKIP LOCKED LIMIT 1").WithArgs("account-a", "railway", "primary").WillReturnRows(sqlmock.NewRows(computeSlotColumns()))
	mock.ExpectRollback()
	_, err = store.BeginLogicalBoxCreation(context.Background(), Principal{AccountID: "account-a", UserID: "user-a"}, v1.CreateLogicalBoxRequest{Name: "research", Provider: "railway", ProviderCredential: "primary"})
	if err == nil || !strings.Contains(err.Error(), "no healthy free compute slot") {
		t.Fatalf("error=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRemainingScaleDownCountsDrainingSlotOnlyOnce(t *testing.T) {
	slots := []v1.ComputeSlot{
		{Ordinal: 1, State: v1.FleetSlotFree},
		{Ordinal: 2, State: v1.FleetSlotOccupied},
		{Ordinal: 3, State: v1.FleetSlotDraining},
		{Ordinal: 4, State: v1.FleetSlotOccupied},
	}
	if got := remainingScaleDown(4, 2, slots); got != 1 {
		t.Fatalf("remainingScaleDown()=%d, want 1", got)
	}
	if got := remainingScaleDown(4, 3, slots); got != 1 {
		t.Fatalf("remainingScaleDown()=%d, want 1 idle slot removed", got)
	}
}

func TestScaleDownRemovesIdleCapacityDespiteWorkspaceDrain(t *testing.T) {
	slots := []v1.ComputeSlot{
		{Ordinal: 1, State: v1.FleetSlotDraining},
		{Ordinal: 2, State: v1.FleetSlotOccupied},
		{Ordinal: 3, State: v1.FleetSlotFree},
	}
	if got := remainingScaleDown(3, 2, slots); got != 1 {
		t.Fatalf("remainingScaleDown()=%d, want temporary idle slot removed", got)
	}
	slots[2].State = v1.FleetSlotOccupied
	if got := remainingScaleDown(3, 2, slots); got != 0 {
		t.Fatalf("remainingScaleDown()=%d, must not drain another occupied slot", got)
	}
}

func TestCreationDoesNotFallBackFromRequestedRegion(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{DB: db}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT claude_version,codex_version,opencode_version FROM agent_cli_versions").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"claude_version", "codex_version", "opencode_version"}))
	mock.ExpectQuery("SELECT id::text FROM logical_boxes").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`FROM compute_slots.*AND s.region=\$4.*FOR UPDATE OF s SKIP LOCKED LIMIT 1`).WithArgs("account-a", "railway", "primary", "requested-region").WillReturnRows(sqlmock.NewRows(computeSlotColumns()))
	mock.ExpectRollback()
	_, err = store.BeginLogicalBoxCreation(context.Background(), Principal{AccountID: "account-a", UserID: "user-a"}, v1.CreateLogicalBoxRequest{Name: "regional", Provider: "railway", ProviderCredential: "primary", Region: "requested-region"})
	if err == nil {
		t.Fatal("unexpected creation without regional capacity")
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRepairableSlotStateRetriesOnlyIncompleteCapacity(t *testing.T) {
	for _, state := range []v1.FleetSlotState{v1.FleetSlotStarting, v1.FleetSlotStopped, v1.FleetSlotUnhealthy} {
		if !repairableSlotState(state) {
			t.Fatalf("state %q should be repaired", state)
		}
	}
	for _, state := range []v1.FleetSlotState{v1.FleetSlotFree, v1.FleetSlotReserved, v1.FleetSlotOccupied, v1.FleetSlotDraining} {
		if repairableSlotState(state) {
			t.Fatalf("state %q must not be redeployed", state)
		}
	}
}
