package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

type workerSettingsTestProvider struct {
	fakeProvider
	config provider.WorkerConfig
	sets   []provider.WorkerSettings
}

func (p *workerSettingsTestProvider) WorkerConfig(context.Context) (provider.WorkerConfig, error) {
	return p.config, nil
}

func (p *workerSettingsTestProvider) SetWorkerSettings(_ context.Context, settings provider.WorkerSettings) (provider.WorkerConfig, error) {
	p.sets = append(p.sets, settings)
	settings.Revision++
	p.config.Settings = settings
	return p.config, nil
}

func testWorkerProvider() *workerSettingsTestProvider {
	return &workerSettingsTestProvider{config: provider.WorkerConfig{
		Settings:      provider.WorkerSettings{Revision: 4, Slots: 2, BoxDefaults: provider.BoxLimits{CPU: 1, MemoryMiB: 2048, SwapMiB: 1024}},
		Specs:         provider.WorkerSpecs{CPUs: 4, MemoryBytes: 8 << 30, SwapBytes: 4 << 30},
		Limits:        provider.WorkerLimits{MinSlots: 2, MaxSlots: 8, PerBoxLimits: true, BoxMin: provider.BoxLimits{CPU: 0.5, MemoryMiB: 1024}, BoxMax: provider.BoxLimits{CPU: 4, MemoryMiB: 8192, SwapMiB: 4096}, CPUStep: 0.5, MemoryStepMiB: 1024},
		SlotsInUse:    2,
		OccupiedSlots: 1,
	}}
}

func putWorker(t *testing.T, server *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/v1/fleet/worker", strings.NewReader(body))
	rec := httptest.NewRecorder()
	server.setFleetWorker(rec, req, Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"})
	return rec
}

func TestSetWorkerPoolRaisesWorkerAndFleetTogether(t *testing.T) {
	store, mock := testStore(t)
	prov := testWorkerProvider()
	server := NewServer(store, provider.NewRegistry())
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return prov, nil }
	mock.ExpectQuery("INSERT INTO fleet_settings").WithArgs("account-a", "shared-worker", "vps", 4).
		WillReturnRows(sqlmock.NewRows([]string{"provider", "provider_credential", "compute_box_slots", "updated_at"}).AddRow("shared-worker", "vps", 4, time.Now()))
	mock.ExpectExec("INSERT INTO audit_log.*fleet.slots.set").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO audit_log.*provider.worker.settings.set").WithArgs("account-a", "user-a", "shared-worker:vps", 4, 1.5, int64(3072), int64(0)).WillReturnResult(sqlmock.NewResult(0, 1))
	rec := putWorker(t, server, `{"provider":"shared-worker","providerCredential":"vps","revision":4,"slots":4,"boxDefaults":{"cpu":1.5,"memoryMiB":3072,"swapMiB":0}}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(prov.sets) != 1 || prov.sets[0].Slots != 4 || prov.sets[0].Revision != 4 || prov.sets[0].BoxDefaults.CPU != 1.5 {
		t.Fatalf("worker settings = %+v", prov.sets)
	}
	var pool workerPool
	if err := json.Unmarshal(rec.Body.Bytes(), &pool); err != nil || pool.DesiredSlots != 4 || pool.Worker.Settings.Slots != 4 {
		t.Fatalf("pool = %+v, %v", pool, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSetWorkerPoolRejectsBeforeChangingAnything(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		want       string
	}{
		{"stale revision", `{"provider":"shared-worker","revision":3,"slots":3}`, 409, "reload"},
		{"below occupied", `{"provider":"shared-worker","revision":4,"slots":0}`, 400, "1–8 slots"},
		{"above machine", `{"provider":"shared-worker","revision":4,"slots":9}`, 400, "1–8 slots"},
		{"default above machine", `{"provider":"shared-worker","revision":4,"slots":3,"boxDefaults":{"cpu":1,"memoryMiB":16384,"swapMiB":0}}`, 400, "memory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := testStore(t)
			prov := testWorkerProvider()
			server := NewServer(store, provider.NewRegistry())
			server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return prov, nil }
			rec := putWorker(t, server, tc.body)
			if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.want) || len(prov.sets) != 0 {
				t.Fatalf("%d %s sets=%v", rec.Code, rec.Body, prov.sets)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSetWorkerPoolKeepsOccupiedSlots(t *testing.T) {
	store, _ := testStore(t)
	prov := testWorkerProvider()
	prov.config.OccupiedSlots = 2
	server := NewServer(store, provider.NewRegistry())
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return prov, nil }
	rec := putWorker(t, server, `{"provider":"shared-worker","revision":4,"slots":1}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "2 boxes") || len(prov.sets) != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestFleetWorkerReportsUnsupportedProviders(t *testing.T) {
	store, mock := testStore(t)
	server := NewServer(store, provider.NewRegistry())
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return &fakeProvider{}, nil }
	mock.ExpectExec("INSERT INTO fleet_settings").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT provider,provider_credential,compute_box_slots").WillReturnRows(sqlmock.NewRows([]string{"provider", "provider_credential", "compute_box_slots", "updated_at", "region"}).AddRow("railway", "main", 2, time.Now(), ""))
	req := httptest.NewRequest("GET", "/v1/fleet/worker?provider=railway&providerCredential=main", nil)
	rec := httptest.NewRecorder()
	server.fleetWorker(rec, req, Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"})
	var pool workerPool
	if err := json.Unmarshal(rec.Body.Bytes(), &pool); rec.Code != 200 || err != nil || pool.Supported || pool.Reason == "" || pool.DesiredSlots != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestSharedBoxLimitsFollowWorkerMachine(t *testing.T) {
	prov := testWorkerProvider()
	for _, tc := range []struct {
		box provider.BoxLimits
		ok  bool
	}{
		{provider.BoxLimits{CPU: 3, MemoryMiB: 6144, SwapMiB: 2048}, true},
		{provider.BoxLimits{CPU: 4.5, MemoryMiB: 2048, SwapMiB: 0}, false},
		{provider.BoxLimits{CPU: 1, MemoryMiB: 9216, SwapMiB: 0}, false},
	} {
		if err := sharedBoxLimitsAllowed(context.Background(), prov, tc.box); (err == nil) != tc.ok {
			t.Errorf("%+v: err = %v", tc.box, err)
		}
	}
	// Older workers keep the original fixed bounds.
	if err := sharedBoxLimitsAllowed(context.Background(), &fakeProvider{}, provider.BoxLimits{CPU: 2, MemoryMiB: 2048}); err == nil {
		t.Fatal("legacy worker accepted 2 CPUs")
	}
	if err := sharedBoxLimitsAllowed(context.Background(), &fakeProvider{}, provider.BoxLimits{CPU: 1, MemoryMiB: 4096, SwapMiB: 1024}); err != nil {
		t.Fatal(fmt.Errorf("legacy worker rejected valid limits: %w", err))
	}
}
