package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

type costBatchFixture struct {
	fakeProvider
	calls int
	now   time.Time
}

func (p *costBatchFixture) UsageBatch(_ context.Context, ids []string) (map[string]provider.Usage, error) {
	p.calls++
	return map[string]provider.Usage{
		ids[0]: {ObservedAt: p.now, Cost: provider.Cost{Currency: "USD", Accrued: 1.25, Available: true}},
		ids[1]: {ObservedAt: p.now, Cost: provider.Cost{Currency: "USD", Accrued: 2.50, Available: true}},
	}, nil
}

func TestFleetCostsBatchesAndSumsSlotUsage(t *testing.T) {
	store, mock := testStore(t)
	now := time.Now().UTC()
	mock.ExpectExec(`INSERT INTO fleet_settings`).WithArgs("account-a", "fake", "primary", v1.DefaultComputeBoxSlots).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT provider,provider_credential,compute_box_slots,updated_at,region FROM fleet_settings`).WithArgs("account-a", "fake", "primary").WillReturnRows(sqlmock.NewRows([]string{"provider", "provider_credential", "compute_box_slots", "updated_at", "region"}).AddRow("fake", "primary", 2, now, "eu"))
	mock.ExpectQuery(`FROM compute_slots`).WithArgs("account-a", "fake", "primary").WillReturnRows(sqlmock.NewRows(computeSlotColumns()).
		AddRow("slot-1", "account-a", "fake", "primary", 1, "occupied", "service-1", "worker-1", "deployment-1", "box-1", "work", "eu", "image", "v1", "healthy", int64(1), "", nil, "", now, now).
		AddRow("slot-2", "account-a", "fake", "primary", 2, "free", "service-2", "worker-2", "deployment-2", "", "", "eu", "image", "v1", "healthy", int64(1), "", nil, "", now, now))
	mock.ExpectQuery(`FROM logical_boxes.*slot_id IS NULL`).WithArgs("account-a", "fake", "primary").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`SELECT count\(\*\) FROM allocation_requests`).WithArgs("account-a", "fake", "primary").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	fixture := &costBatchFixture{now: now}
	server := NewServer(store, nil)
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return fixture, nil }
	request := httptest.NewRequest(http.MethodGet, "/v1/fleet/costs?provider=fake&providerCredential=primary", nil)
	response := httptest.NewRecorder()
	server.fleetCosts(response, request, Principal{AccountID: "account-a", Role: "owner"})
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var overview v1.FleetCostOverview
	if err := json.Unmarshal(response.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if fixture.calls != 1 || !overview.Total.Available || overview.Total.Accrued != 3.75 || overview.AvailableSlotCount != 2 {
		t.Fatalf("calls=%d overview=%+v", fixture.calls, overview)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control = %q", response.Header().Get("Cache-Control"))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
