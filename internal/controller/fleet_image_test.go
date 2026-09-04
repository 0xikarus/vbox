package controller

import (
	"context"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestRefreshFleetSlotImagesPersistsProviderObservationWithoutDeploy(t *testing.T) {
	store, mock := testStore(t)
	prov := &fakeProvider{boxes: []provider.Box{{ID: "service-1", Image: "worker@sha256:observed"}}}
	status := v1.FleetStatus{Slots: []v1.ComputeSlot{{ID: "slot-1", ServiceID: "service-1", Ordinal: 1, Image: "worker@sha256:stale", State: v1.FleetSlotFree}}}
	mock.ExpectExec("UPDATE compute_slots SET image").
		WithArgs("account-a", "slot-1", "worker@sha256:observed", "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	server := NewServer(store, provider.NewRegistry(prov))
	if err := server.refreshFleetSlotImages(context.Background(), "account-a", &status, prov); err != nil {
		t.Fatal(err)
	}
	if status.Slots[0].Image != "worker@sha256:observed" || len(prov.argv) != 0 || prov.created.Name != "" {
		t.Fatalf("refresh mutated provider workload: status=%+v argv=%v created=%+v", status, prov.argv, prov.created)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
