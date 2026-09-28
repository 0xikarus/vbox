package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestAvailableAgentBoxWorkersIncludeOtherAccountPools(t *testing.T) {
	store, mock := testStore(t)
	grant, _ := json.Marshal(v1.CreateAgentBoxGrant{Enabled: true, MaxBoxes: 2, MaxDiskGiB: 50, AllowedAgents: []string{"codex"}})
	mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionCreateAgentBox, grant))
	mock.ExpectQuery("SELECT provider,provider_credential FROM logical_boxes.*state='running'").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"provider", "provider_credential"}).AddRow("railway", "primary"))
	mock.ExpectQuery("(?s)FROM compute_slots s.*JOIN provider_credentials.*JOIN fleet_settings.*s.state='free'.*s.health='healthy'.*NOT EXISTS").WithArgs("account-a", "railway", "primary").
		WillReturnRows(sqlmock.NewRows([]string{"id", "provider", "provider_credential", "service_name", "ordinal", "region"}).
			AddRow("slot-2", "railway", "primary", "worker-two", 2, "eu").
			AddRow("slot-3", "shared-worker", "other", "worker-three", 1, "us"))
	r := httptest.NewRequest(http.MethodGet, "/v1/agent-desktop/available-workers", nil)
	w := httptest.NewRecorder()
	(&Server{Store: store}).agentBoxWorkersHandler(w, r, Principal{AccountID: "account-a", Subject: "desktop-box:box-a"})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var workers []agentBoxWorker
	if err := json.Unmarshal(w.Body.Bytes(), &workers); err != nil {
		t.Fatal(err)
	}
	if len(workers) != 2 || workers[0].SlotID != "slot-2" || workers[0].ServiceName != "worker-two" || workers[1].Provider != "shared-worker" || workers[1].ProviderCredential != "other" {
		t.Fatalf("workers=%+v", workers)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentBoxPlacementCandidatesCanUseAnotherPool(t *testing.T) {
	for _, test := range []struct {
		name   string
		slotID string
	}{
		{name: "explicit other pool", slotID: "slot-other"},
		{name: "automatic fallback", slotID: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, mock := testStore(t)
			mock.ExpectQuery("(?s)FROM compute_slots s.*JOIN provider_credentials.*JOIN fleet_settings.*NOT EXISTS").WithArgs("account-a", "shared-worker", "creator").
				WillReturnRows(sqlmock.NewRows([]string{"id", "provider", "provider_credential", "service_name", "ordinal", "region"}).
					AddRow("slot-other", "shared-worker", "other", "other-worker", 1, "us"))
			workers, err := store.agentBoxPlacementCandidates(context.Background(), "account-a", "shared-worker", "creator", test.slotID)
			if err != nil || len(workers) != 1 || workers[0].Provider != "shared-worker" || workers[0].ProviderCredential != "other" || workers[0].SlotID != "slot-other" {
				t.Fatalf("workers=%+v error=%v", workers, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAutomaticAgentBoxPlacementOffersEachPoolOnce(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("(?s)FROM compute_slots s.*JOIN provider_credentials.*JOIN fleet_settings.*NOT EXISTS").WithArgs("account-a", "shared-worker", "creator").
		WillReturnRows(sqlmock.NewRows([]string{"id", "provider", "provider_credential", "service_name", "ordinal", "region"}).
			AddRow("slot-1", "shared-worker", "creator", "creator-worker", 1, "us").
			AddRow("slot-2", "shared-worker", "creator", "creator-worker", 2, "us").
			AddRow("slot-3", "shared-worker", "other", "other-worker", 1, "eu"))
	candidates, err := store.agentBoxPlacementCandidates(context.Background(), "account-a", "shared-worker", "creator", "")
	if err != nil || len(candidates) != 2 || candidates[0].ProviderCredential != "creator" || candidates[1].ProviderCredential != "other" {
		t.Fatalf("candidates=%+v error=%v", candidates, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAvailableAgentBoxWorkersRequireCreateGrant(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}))
	r := httptest.NewRequest(http.MethodGet, "/v1/agent-desktop/available-workers", nil)
	w := httptest.NewRecorder()
	(&Server{Store: store}).agentBoxWorkersHandler(w, r, Principal{AccountID: "account-a", Subject: "desktop-box:box-a"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
