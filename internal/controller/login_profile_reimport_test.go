package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestValidateBoxProfileRefsRejectsDuplicateOversizedAndUnavailable(t *testing.T) {
	s, _ := testStore(t)
	server := &Server{Store: s}
	twoProfiles := []v1.LoginProfileRef{{Application: "claude", Name: "work"}, {Application: "codex", Name: "work"}}
	if err := server.validateBoxProfileRefs(context.Background(), "a", twoProfiles); err == nil || !strings.Contains(err.Error(), "select at most 1 login profile") {
		t.Fatalf("multiple profile selection was not rejected by the one-profile limit: %v", err)
	}
	duplicate := []v1.LoginProfileRef{{Application: "claude", Name: "work"}, {Application: "claude", Name: "work"}}
	if err := server.validateBoxProfileRefs(context.Background(), "a", duplicate); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate profile selection accepted: %v", err)
	}
	oversized := make([]v1.LoginProfileRef, 0, 9)
	for i := 0; i < 9; i++ {
		oversized = append(oversized, v1.LoginProfileRef{Application: "codex", Name: string(rune('a' + i))})
	}
	if err := server.validateBoxProfileRefs(context.Background(), "a", oversized); err == nil {
		t.Fatal("oversized profile selection accepted")
	}
	// No encryption envelope: the profile cannot be loaded, so it must be rejected.
	if err := server.validateBoxProfileRefs(context.Background(), "a", []v1.LoginProfileRef{{Application: "codex", Name: "work"}}); err == nil {
		t.Fatal("unavailable profile accepted")
	}
}

func TestCreateLogicalBoxRejectsMultipleLoginProfiles(t *testing.T) {
	s, _ := testStore(t)
	server := &Server{Store: s}
	request := httptest.NewRequest(http.MethodPost, "/v1/logical-boxes", strings.NewReader(`{
		"name":"one-profile-only",
		"loginProfiles":[
			{"application":"claude","name":"work"},
			{"application":"codex","name":"work"}
		]
	}`))
	response := httptest.NewRecorder()

	server.createLogicalBoxHandler(response, request, Principal{AccountID: "a", UserID: "u", Role: "owner"})

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "select at most 1 login profile") {
		t.Fatalf("multiple profile creation status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestBoxLoginProfileStateReadsImportedPendingAndVerified(t *testing.T) {
	s, m := testStore(t)
	imported, _ := json.Marshal([]v1.LoginProfileRef{{Application: "codex", Name: "work"}})
	pending, _ := json.Marshal([]v1.LoginProfileRef{{Application: "claude", Name: "personal"}})
	m.ExpectQuery(`SELECT COALESCE\(metadata->'importedLoginProfiles'`).WithArgs("a", "box-1").
		WillReturnRows(sqlmock.NewRows([]string{"imported", "verified", "pending"}).AddRow(imported, true, pending))
	server := &Server{Store: s}
	state, err := server.boxLoginProfileState(context.Background(), "a", "box-1")
	if err != nil || !state.Verified || len(state.Imported) != 1 || state.Imported[0].Name != "work" || len(state.Pending) != 1 || state.Pending[0].Application != "claude" {
		t.Fatalf("state projection: %v %+v", err, state)
	}
	// A missing box is an error, never fabricated state.
	m.ExpectQuery(`SELECT COALESCE\(metadata->'importedLoginProfiles'`).WithArgs("a", "ghost").WillReturnError(sql.ErrNoRows)
	if _, err := server.boxLoginProfileState(context.Background(), "a", "ghost"); err == nil {
		t.Fatal("missing box reported credential state")
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionPendingBoxProfilesSkipsWithoutPending(t *testing.T) {
	s, m := testStore(t)
	m.ExpectQuery(`SELECT COALESCE\(metadata->'importedLoginProfiles'`).WithArgs("a", "box-1").
		WillReturnRows(sqlmock.NewRows([]string{"imported", "verified", "pending"}).AddRow([]byte(`[]`), true, []byte(`[]`)))
	server := &Server{Store: s}
	if err := server.provisionPendingBoxProfiles(context.Background(), nil, "a", fleetAssignment{Box: v1.LogicalBox{ID: "box-1", AccountID: "a"}}); err != nil {
		t.Fatalf("no pending selection must be a no-op: %v", err)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPutBoxLoginProfilesRejectsUnknownBox(t *testing.T) {
	s, m := testStore(t)
	m.ExpectQuery(`SELECT id::text,account_id::text,owner_user_id::text,name,provider`).WithArgs("a", "ghost").
		WillReturnError(sql.ErrNoRows)
	server := &Server{Store: s}
	request := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"profiles":[{"application":"codex","name":"work"}]}`))
	request.SetPathValue("id", "ghost")
	response := httptest.NewRecorder()
	server.putBoxLoginProfiles(response, request, Principal{AccountID: "a", UserID: "u", Role: "owner"})
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown box status=%d body=%s", response.Code, response.Body.String())
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPutBoxLoginProfilesRejectsInvalidPayload(t *testing.T) {
	s, _ := testStore(t)
	server := &Server{Store: s}
	request := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"profiles":"not-a-list"}`))
	request.SetPathValue("id", "box-1")
	response := httptest.NewRecorder()
	server.putBoxLoginProfiles(response, request, Principal{AccountID: "a", UserID: "u", Role: "owner"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid payload status=%d", response.Code)
	}
}
