package controller

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

func expectProviderPlan(mock sqlmock.Sqlmock, deleting, isDefault bool, boxes, slots *sqlmock.Rows) {
	mock.ExpectQuery(`SELECT deleting FROM provider_credentials`).WithArgs("account-a", "railway", "primary").WillReturnRows(sqlmock.NewRows([]string{"deleting"}).AddRow(deleting))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM controller_defaults`).WithArgs("account-a", "railway", "primary").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(isDefault))
	mock.ExpectQuery(`SELECT id::text,name,state FROM logical_boxes`).WithArgs("account-a", "railway", "primary").WillReturnRows(boxes)
	mock.ExpectQuery(`SELECT id::text,state,COALESCE\(service_id`).WithArgs("account-a", "railway", "primary").WillReturnRows(slots)
}
func deleteFixture(t *testing.T) (*Server, sqlmock.Sqlmock, Principal) {
	t.Helper()
	store, mock := testStore(t)
	return NewServer(store, provider.NewRegistry()), mock, Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"}
}
func deleteRequest(body []byte) *http.Request {
	r := httptest.NewRequest(http.MethodDelete, "/v1/provider-credentials/railway/primary", bytes.NewReader(body))
	r.SetPathValue("provider", "railway")
	r.SetPathValue("name", "primary")
	return r
}
func TestProviderDeletePlanListsBoxesAndCloudServers(t *testing.T) {
	server, mock, _ := deleteFixture(t)
	expectProviderPlan(mock, false, true, sqlmock.NewRows([]string{"id", "name", "state"}).AddRow("box-1", "sleeping", "hibernated"), sqlmock.NewRows([]string{"id", "state", "service_id", "service_name"}).AddRow("slot-1", "free", "srv-1", "worker"))
	plan, err := server.Store.providerDeletePlan(context.Background(), "account-a", "railway", "primary")
	if err != nil {
		t.Fatal(err)
	}
	if plan.CanDelete || !plan.IsDefault || len(plan.Boxes) != 1 || plan.Boxes[0].State != "hibernated" || len(plan.Slots) != 1 || plan.CloudServers != 1 || len(plan.Blockers) == 0 {
		t.Fatalf("bad plan: %+v", plan)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestProviderDeletePlanBlocksActiveSlot(t *testing.T) {
	server, mock, _ := deleteFixture(t)
	expectProviderPlan(mock, false, false, sqlmock.NewRows([]string{"id", "name", "state"}), sqlmock.NewRows([]string{"id", "state", "service_id", "service_name"}).AddRow("slot-1", "reserved", "srv-1", "worker"))
	plan, err := server.Store.providerDeletePlan(context.Background(), "account-a", "railway", "primary")
	if err != nil {
		t.Fatal(err)
	}
	if plan.CanDelete || len(plan.Blockers) != 1 || plan.Workers != 1 {
		t.Fatalf("bad active-slot plan: %+v", plan)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestProviderDeleteBlockedByBox(t *testing.T) {
	server, mock, owner := deleteFixture(t)
	mock.ExpectExec(`UPDATE provider_credentials SET deleting=true`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectProviderPlan(mock, true, false, sqlmock.NewRows([]string{"id", "name", "state"}).AddRow("box-1", "sleeping", "hibernated"), sqlmock.NewRows([]string{"id", "state", "service_id", "service_name"}))
	mock.ExpectExec(`UPDATE provider_credentials SET deleting=false`).WillReturnResult(sqlmock.NewResult(0, 1))
	w := httptest.NewRecorder()
	server.deleteProviderCredential(w, deleteRequest(nil), owner)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestProviderDeleteDefaultRequiresReplacement(t *testing.T) {
	server, mock, owner := deleteFixture(t)
	mock.ExpectExec(`UPDATE provider_credentials SET deleting=true`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectProviderPlan(mock, true, true, sqlmock.NewRows([]string{"id", "name", "state"}), sqlmock.NewRows([]string{"id", "state", "service_id", "service_name"}))
	mock.ExpectExec(`UPDATE provider_credentials SET deleting=false`).WillReturnResult(sqlmock.NewResult(0, 1))
	w := httptest.NewRecorder()
	server.deleteProviderCredential(w, deleteRequest(nil), owner)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestProviderDeleteRemovesCredentialAndFleetConfig(t *testing.T) {
	server, mock, owner := deleteFixture(t)
	mock.ExpectExec(`UPDATE provider_credentials SET deleting=true`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectProviderPlan(mock, true, true, sqlmock.NewRows([]string{"id", "name", "state"}), sqlmock.NewRows([]string{"id", "state", "service_id", "service_name"}))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT deleting FROM provider_credentials.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"deleting"}).AddRow(true))
	mock.ExpectQuery(`SELECT count\(\*\) FROM logical_boxes`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT count\(\*\) FROM compute_slots`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT provider,name FROM provider_credentials`).WithArgs("account-a", "railway", "primary").WillReturnRows(sqlmock.NewRows([]string{"provider", "name"}))
	mock.ExpectExec(`DELETE FROM controller_defaults`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM compute_slots`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM fleet_settings`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM provider_credentials`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO audit_log`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	w := httptest.NewRecorder()
	server.deleteProviderCredential(w, deleteRequest([]byte(`{"newDefault":null}`)), owner)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProviderDeleteMovesDefaultToSoleRemainingCredential(t *testing.T) {
	server, mock, owner := deleteFixture(t)
	mock.ExpectExec(`UPDATE provider_credentials SET deleting=true`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectProviderPlan(mock, true, true, sqlmock.NewRows([]string{"id", "name", "state"}), sqlmock.NewRows([]string{"id", "state", "service_id", "service_name"}))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT deleting FROM provider_credentials.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"deleting"}).AddRow(true))
	mock.ExpectQuery(`SELECT count\(\*\) FROM logical_boxes`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT count\(\*\) FROM compute_slots`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT provider,name FROM provider_credentials`).WithArgs("account-a", "railway", "primary").WillReturnRows(sqlmock.NewRows([]string{"provider", "name"}).AddRow("shared-worker", "local"))
	mock.ExpectExec(`UPDATE controller_defaults SET provider`).WithArgs("account-a", "railway", "primary", "shared-worker", "local").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM compute_slots`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM fleet_settings`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM provider_credentials`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO audit_log`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	w := httptest.NewRecorder()
	server.deleteProviderCredential(w, deleteRequest([]byte(`{"newDefault":null}`)), owner)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProviderDeleteClearsDefaultWhenMultipleCredentialsRemain(t *testing.T) {
	server, mock, owner := deleteFixture(t)
	mock.ExpectExec(`UPDATE provider_credentials SET deleting=true`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectProviderPlan(mock, true, true, sqlmock.NewRows([]string{"id", "name", "state"}), sqlmock.NewRows([]string{"id", "state", "service_id", "service_name"}))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT deleting FROM provider_credentials.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"deleting"}).AddRow(true))
	mock.ExpectQuery(`SELECT count\(\*\) FROM logical_boxes`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT count\(\*\) FROM compute_slots`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT provider,name FROM provider_credentials`).WithArgs("account-a", "railway", "primary").WillReturnRows(sqlmock.NewRows([]string{"provider", "name"}).AddRow("shared-worker", "local").AddRow("railway", "backup"))
	mock.ExpectExec(`DELETE FROM controller_defaults`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM compute_slots`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM fleet_settings`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM provider_credentials`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO audit_log`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	w := httptest.NewRecorder()
	server.deleteProviderCredential(w, deleteRequest([]byte(`{"newDefault":null}`)), owner)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

type missingProviderService struct {
	fakeProvider
	deleted bool
}

func (*missingProviderService) Inspect(context.Context, string) (provider.Box, error) {
	return provider.Box{}, provider.ErrNotFound
}
func (p *missingProviderService) Delete(context.Context, string, provider.Owner) error {
	p.deleted = true
	return nil
}

func TestProviderDeleteRetriesAfterServiceAlreadyGone(t *testing.T) {
	server, mock, owner := deleteFixture(t)
	fixture := &missingProviderService{}
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return fixture, nil }
	mock.ExpectExec(`UPDATE provider_credentials SET deleting=true`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectProviderPlan(mock, true, false, sqlmock.NewRows([]string{"id", "name", "state"}), sqlmock.NewRows([]string{"id", "state", "service_id", "service_name"}).AddRow("slot-1", "free", "gone-service", "worker"))
	mock.ExpectQuery(`UPDATE compute_slots SET state='deprovisioning'`).WithArgs("account-a", "slot-1", "free").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("slot-1"))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT deleting FROM provider_credentials.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"deleting"}).AddRow(true))
	mock.ExpectQuery(`SELECT count\(\*\) FROM logical_boxes`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT count\(\*\) FROM compute_slots`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`DELETE FROM compute_slots`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM fleet_settings`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM provider_credentials`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO audit_log`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	w := httptest.NewRecorder()
	server.deleteProviderCredential(w, deleteRequest(nil), owner)
	if w.Code != http.StatusNoContent || fixture.deleted {
		t.Fatalf("status=%d delete called=%t body=%s", w.Code, fixture.deleted, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProviderDeleteClaimRaceDoesNotDeprovisionReservedSlot(t *testing.T) {
	server, mock, owner := deleteFixture(t)
	fixture := &missingProviderService{}
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return fixture, nil }
	mock.ExpectExec(`UPDATE provider_credentials SET deleting=true`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectProviderPlan(mock, true, false, sqlmock.NewRows([]string{"id", "name", "state"}), sqlmock.NewRows([]string{"id", "state", "service_id", "service_name"}).AddRow("slot-1", "free", "gone-service", "worker-1").AddRow("slot-2", "free", "active-service", "worker-2"))
	mock.ExpectQuery(`UPDATE compute_slots SET state='deprovisioning'`).WithArgs("account-a", "slot-1", "free").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("slot-1"))
	mock.ExpectQuery(`UPDATE compute_slots SET state='deprovisioning'`).WithArgs("account-a", "slot-2", "free").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec(`UPDATE compute_slots SET state=\$3,health=CASE`).WithArgs("account-a", "slot-1", "free", true).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE provider_credentials SET deleting=false`).WillReturnResult(sqlmock.NewResult(0, 1))
	w := httptest.NewRecorder()
	server.deleteProviderCredential(w, deleteRequest(nil), owner)
	if w.Code != http.StatusConflict || fixture.deleted {
		t.Fatalf("status=%d delete called=%t body=%s", w.Code, fixture.deleted, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
