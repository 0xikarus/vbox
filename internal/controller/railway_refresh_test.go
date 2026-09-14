package controller

import (
	"context"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

type hintRefreshProvider struct {
	provider.Provider
	refreshes int
}

func (p *hintRefreshProvider) RefreshInventory(context.Context) error {
	p.refreshes++
	return nil
}

func TestRailwayHintRechecksScopeBeforeInventoryRefresh(t *testing.T) {
	for _, current := range []bool{false, true} {
		store, mock := testStore(t)
		server := NewServer(store, nil)
		backing := &hintRefreshProvider{}
		server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) {
			if !current {
				t.Fatal("stale hint resolved a provider")
			}
			return backing, nil
		}
		mock.ExpectQuery(`SELECT EXISTS`).
			WithArgs("account", "slot", "primary", "service", "project", "environment").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(current))
		err := server.RefreshRailwayHint(context.Background(), RailwayRefreshHint{
			AccountID: "account", SlotID: "slot", ProviderCredential: "primary",
			ServiceID: "service", ProjectID: "project", EnvironmentID: "environment",
		})
		if err != nil || backing.refreshes != map[bool]int{false: 0, true: 1}[current] {
			t.Fatalf("refresh count %d, error %v", backing.refreshes, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}
