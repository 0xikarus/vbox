package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestAvailableAgentBoxWorkersAreScopedAndFree(t *testing.T) {
	store, mock := testStore(t)
	grant, _ := json.Marshal(v1.CreateAgentBoxGrant{Enabled: true, MaxBoxes: 2, MaxDiskGiB: 50, AllowedAgents: []string{"codex"}})
	mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionCreateAgentBox, grant))
	mock.ExpectQuery("SELECT provider,provider_credential FROM logical_boxes.*state='running'").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"provider", "provider_credential"}).AddRow("railway", "primary"))
	mock.ExpectQuery("FROM compute_slots s.*s.state='free'.*s.health='healthy'.*NOT EXISTS").WithArgs("account-a", "railway", "primary").
		WillReturnRows(sqlmock.NewRows([]string{"id", "service_name", "ordinal", "region"}).AddRow("slot-2", "worker-two", 2, "eu"))
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
	if len(workers) != 1 || workers[0].SlotID != "slot-2" || workers[0].ServiceName != "worker-two" {
		t.Fatalf("workers=%+v", workers)
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
