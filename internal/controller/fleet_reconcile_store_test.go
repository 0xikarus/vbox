package controller

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestListFleetConfigsIgnoresEmptyCredentialShadowedByNamedPools(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	mock.ExpectQuery(`FROM fleet_settings f\s+WHERE.*NOT EXISTS`).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "provider", "provider_credential", "compute_box_slots", "updated_at"}).
			AddRow("account-a", "shared-worker", "shared-01", 4, now).
			AddRow("account-a", "shared-worker", "shared-02", 4, now))

	configs, err := (&Store{DB: db}).ListFleetConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 2 || configs[0].Config.ProviderCredential != "shared-01" || configs[1].Config.ProviderCredential != "shared-02" {
		t.Fatalf("configs=%+v", configs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
