package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

type slowHibernateProvider struct {
	fakeProvider
	started   chan struct{}
	release   chan struct{}
	detached  bool
	sanitized bool
}

func (p *slowHibernateProvider) Exec(context.Context, string, []string, provider.ExecOptions) (provider.ExecResult, error) {
	close(p.started)
	<-p.release
	return provider.ExecResult{}, nil
}

func (p *slowHibernateProvider) DetachStorage(context.Context, string, provider.Storage) error {
	p.detached = true
	return nil
}

func (*slowHibernateProvider) AttachedStorage(context.Context, string) (*provider.Storage, error) {
	return nil, nil
}

func (p *slowHibernateProvider) SanitizeSlot(context.Context, string) error {
	p.sanitized = true
	return nil
}

func occupiedSlotRow(now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(computeSlotColumns()).AddRow(
		"slot-1", "account-a", "railway", "primary", 1, "occupied", "service-1", "slot-a-01",
		"deployment-1", "box-1", "research", "iad", "worker@sha256:digest", "", "healthy",
		int64(3), "cli", now.Add(time.Minute), "", now, now,
	)
}

func expectBeginHibernate(mock sqlmock.Sqlmock, now time.Time) {
	mock.ExpectBegin()
	mock.ExpectQuery("FROM logical_boxes.*FOR UPDATE").
		WithArgs("account-a", "box-1").
		WillReturnRows(logicalBoxRowWithSlot(v1.LogicalBoxRunning, "claude", "slot-1"))
	mock.ExpectQuery("SELECT COALESCE\\(fencing_token").
		WithArgs("account-a", "box-1").
		WillReturnRows(sqlmock.NewRows([]string{"fencing_token"}).AddRow("fence-1"))
	mock.ExpectQuery("FROM compute_slots.*FOR UPDATE OF s").
		WithArgs("account-a", "slot-1").
		WillReturnRows(occupiedSlotRow(now))
	mock.ExpectExec("UPDATE logical_boxes SET state=\\$5.*restoration_state=CASE").
		WithArgs("account-a", "box-1", int64(3), "fence-1", v1.LogicalBoxHibernating, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE compute_slots SET state='draining'").
		WithArgs("account-a", "slot-1", int64(3), "fence-1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func TestHibernateHandlerSurvivesRequestCancellationDuringSlowFlush(t *testing.T) {
	store, mock := testStore(t)
	expectBeginHibernate(mock, time.Now().UTC())
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	finished := make(chan struct{})
	server := NewServer(store, nil)
	server.StartHibernate = func(ctx context.Context, _ Principal, _ string) error {
		started <- ctx
		<-release
		close(finished)
		return nil
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/v1/logical-boxes/box-1/hibernate", nil).WithContext(requestCtx)
	request.SetPathValue("id", "box-1")
	response := httptest.NewRecorder()
	server.hibernateLogicalBoxHandler(response, request, Principal{AccountID: "account-a", UserID: "user-a", Role: "user"})
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var box v1.LogicalBox
	if err := json.Unmarshal(response.Body.Bytes(), &box); err != nil {
		t.Fatal(err)
	}
	if box.State != v1.LogicalBoxHibernating || box.RestorationState != "hibernate-queued" {
		t.Fatalf("box=%+v", box)
	}
	var operationCtx context.Context
	select {
	case operationCtx = <-started:
	case <-time.After(time.Second):
		t.Fatal("hibernate operation was not handed off")
	}
	cancelRequest()
	select {
	case <-operationCtx.Done():
		t.Fatal("request cancellation reached the durable hibernate operation")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("slow hibernate operation did not finish")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteLogicalBoxHibernateWaitsForSlowFlushBeforeDetaching(t *testing.T) {
	store, mock := testStore(t)
	for _, phase := range []string{"saving-workspace", "detaching-volume", "verifying-detach", "sanitizing-compute"} {
		mock.ExpectExec("UPDATE logical_boxes SET restoration_state").
			WithArgs("account-a", "box-1", "claim-1", phase).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT slot_id::text FROM logical_boxes").
		WithArgs("account-a", "box-1", int64(3), "fence-1").
		WillReturnRows(sqlmock.NewRows([]string{"slot_id"}).AddRow("slot-1"))
	mock.ExpectExec("UPDATE compute_slots SET state='free'").
		WithArgs("account-a", "slot-1", int64(3), "fence-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE logical_boxes SET state=\\$5,restoration_state='saved'").
		WithArgs("account-a", "box-1", int64(3), "fence-1", v1.LogicalBoxHibernated).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").
		WillReturnRows(logicalBoxRowWithSlot(v1.LogicalBoxHibernated, "claude", ""))
	prov := &slowHibernateProvider{started: make(chan struct{}), release: make(chan struct{})}
	server := NewServer(store, provider.NewRegistry(prov))
	assignment := fleetAssignment{
		Box:          v1.LogicalBox{ID: "box-1", Name: "research", Provider: "fake", ProviderCredential: "primary", VolumeID: "volume-1", VolumeName: "research-data", AssignmentGeneration: 3},
		Slot:         v1.ComputeSlot{ID: "slot-1", ServiceID: "service-1"},
		FencingToken: "fence-1",
	}
	done := make(chan error, 1)
	go func() {
		_, err := server.completeLogicalBoxHibernate(context.Background(), Principal{AccountID: "account-a", UserID: "user-a", Role: "user"}, assignment, "claim-1")
		done <- err
	}()
	select {
	case <-prov.started:
	case <-time.After(time.Second):
		t.Fatal("prepare-hibernate did not start")
	}
	select {
	case err := <-done:
		t.Fatalf("hibernate detached before slow flush completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if prov.detached {
		t.Fatal("volume detached during active flush")
	}
	close(prov.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("hibernate did not finish after flush")
	}
	if !prov.detached || !prov.sanitized {
		t.Fatalf("detached=%v sanitized=%v", prov.detached, prov.sanitized)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHibernateClaimPreventsConcurrentProviderOperations(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec("UPDATE logical_boxes SET lease_owner").
		WithArgs("account-a", "box-1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE logical_boxes SET lease_owner").
		WithArgs("account-a", "box-1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	claim, claimed, err := store.ClaimLogicalBoxHibernate(context.Background(), "account-a", "box-1")
	if err != nil || !claimed || !strings.HasPrefix(claim, "hibernate_") {
		t.Fatalf("claim=%q claimed=%v err=%v", claim, claimed, err)
	}
	if _, claimed, err := store.ClaimLogicalBoxHibernate(context.Background(), "account-a", "box-1"); err != nil || claimed {
		t.Fatalf("concurrent claimed=%v err=%v", claimed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHibernateReconcilerResumesDurableQueuedState(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("FROM logical_boxes WHERE state='hibernating'").
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "id", "owner_user_id"}).AddRow("account-a", "box-1", "user-a"))
	started := make(chan Principal, 1)
	server := NewServer(store, nil)
	server.StartHibernate = func(_ context.Context, p Principal, id string) error {
		if id != "box-1" {
			t.Errorf("box=%q", id)
		}
		started <- p
		return nil
	}
	if err := server.ReconcileLogicalBoxHibernatesNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-started:
		if p.AccountID != "account-a" || p.UserID != "user-a" {
			t.Fatalf("principal=%+v", p)
		}
	case <-time.After(time.Second):
		t.Fatal("reconciler did not resume hibernate")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
