package controller

import (
	"context"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestEnrolledRailwayWorkerStaysFailClosedWhenRolloutDisabled(t *testing.T) {
	store, mock := testStore(t)
	server := NewServer(store, nil)
	server.DirectWorkersEnabled = false
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) {
		return &noRailwayCallsProvider{}, nil
	}

	wrapped, err := server.provider(context.Background(), "account", "railway", "credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := wrapped.(*directWorkerProvider); !ok {
		t.Fatal("Railway provider was not guarded by enrolled-worker routing")
	}

	mock.ExpectQuery(`SELECT w.id::text,w.account_id::text,w.slot_id::text`).
		WithArgs("account", "railway", "credential", "service").
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "slot_id", "service_id", "incarnation", "connection_epoch", "transport_enabled", "live"}).
			AddRow("worker", "account", "slot", "service", "incarnation", int64(7), true, false))

	if _, err = wrapped.Connection(context.Background(), "service"); err == nil {
		t.Fatal("offline enrolled worker fell back to Railway")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
