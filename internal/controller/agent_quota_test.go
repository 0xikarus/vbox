package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestAgentBoxCreationRejectsOversizedStartupInstructions(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"name":         "child",
		"agent":        "codex",
		"instructions": strings.Repeat("x", v1.MaxInstructionMarkdownBytes+1),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-key")
	request.SetPathValue("id", "box-a")
	response := httptest.NewRecorder()
	(&Server{}).agentBoxCreationHandler(response, request, Principal{AccountID: "account-a", UserID: "user-a"})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "64 KiB") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentBoxCreationRejectsUnsupportedOrDuplicatePresets(t *testing.T) {
	for _, tools := range [][]string{{"unknown"}, {"blender", "blender"}} {
		body, _ := json.Marshal(map[string]any{"name": "child", "agent": "codex", "tools": tools})
		request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "create-key")
		response := httptest.NewRecorder()
		(&Server{}).agentBoxCreationHandler(response, request, Principal{AccountID: "account-a", UserID: "user-a"})
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "supported tool preset") {
			t.Fatalf("tools=%v status=%d body=%s", tools, response.Code, response.Body.String())
		}
	}
}

func TestAgentBoxQuotaCountsPendingReservationsWhileHoldingActorLock(t *testing.T) {
	store, mock := testStore(t)
	grant, err := json.Marshal(v1.CreateAgentBoxGrant{Enabled: true, MaxBoxes: 1, MaxDiskGiB: 50, AllowedAgents: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionCreateAgentBox, grant))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT provider,provider_credential,COALESCE.*FROM logical_boxes.*FOR UPDATE").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"provider", "provider_credential", "region"}).AddRow("railway", "primary", "eu"))
	mock.ExpectQuery("SELECT id::text,requested_name").WithArgs("account-a", "box-a", "create-key").
		WillReturnRows(sqlmock.NewRows([]string{"id", "requested_name", "requested_agent", "requested_disk_gib", "requested_role_ids", "requested_login_profiles", "requested_tools", "requested_instructions", "requested_slot_id", "created_box_id"}))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM agent_box_creations").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()

	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"name":"child","agent":"codex","diskGiB":10}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-key")
	request.SetPathValue("id", "box-a")
	response := httptest.NewRecorder()
	server := &Server{Store: store}
	server.agentBoxCreationHandler(response, request, Principal{AccountID: "account-a", UserID: "user-a"})
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentEmailQuotaCountsProvisioningReservationsWhileHoldingActorLock(t *testing.T) {
	store, mock := testStore(t)
	grant, err := json.Marshal(v1.CreateEmailAddressGrant{Enabled: true, MaxAddresses: 1, Domains: []string{"example.com"}, AddressTypes: []string{"alias"}})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionCreateEmail, grant))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id::text FROM logical_boxes.*FOR UPDATE").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("box-a"))
	mock.ExpectQuery("SELECT id::text,COALESCE\\(address").WithArgs("account-a", "box-a", "email-key").
		WillReturnRows(sqlmock.NewRows([]string{"id", "address", "provider_ref", "state", "failure_reason", "address_type", "domain", "local_part"}))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM agent_email_addresses").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()

	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"domain":"example.com","addressType":"alias"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "email-key")
	request.SetPathValue("id", "box-a")
	response := httptest.NewRecorder()
	server := &Server{Store: store, EmailProvisionURL: "https://provider.invalid"}
	server.agentEmailHandler(response, request, Principal{AccountID: "account-a", UserID: "user-a"})
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
