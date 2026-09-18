package controller

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

// logicalBoxRows builds the row scanLogicalBox expects, in logicalBoxSelect
// order. Only the state matters to these tests; the rest is filler.
func logicalBoxRows(id, state string) *sqlmock.Rows {
	now := time.Now().UTC()
	return sqlmock.NewRows([]string{
		"id", "account_id", "owner_user_id", "name", "provider",
		"provider_credential", "default_agent", "role", "state", "volume_id", "volume_name",
		"slot_id", "assignment_generation", "lease_owner", "lease_expires_at",
		"restoration_state", "failure_reason", "created_at", "updated_at", "tools",
	}).AddRow(
		id, "a", "u", "box", "shared-worker",
		"shared-01", "opencode", "worker", state, "vol", "vol",
		"", 1, "", nil,
		"restored", "", now, now, []byte(`[]`),
	)
}

func TestResyncBoxInstructionsRejectsUnknownBox(t *testing.T) {
	s, m := testStore(t)
	m.ExpectQuery(`SELECT id::text,account_id::text,owner_user_id::text,name,provider`).WithArgs("a", "ghost").
		WillReturnError(sql.ErrNoRows)
	server := &Server{Store: s}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.SetPathValue("id", "ghost")
	response := httptest.NewRecorder()
	server.resyncBoxInstructions(response, request, Principal{AccountID: "a", UserID: "u", Role: "owner"})
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown box status=%d body=%s", response.Code, response.Body.String())
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A stopped box has nothing to push to. Re-sync must say so plainly and must not
// reach for an assignment or a provider, which would surface as a confusing
// transport error instead of the real reason.
func TestResyncBoxInstructionsOnStoppedBoxReportsWithoutProvider(t *testing.T) {
	s, m := testStore(t)
	m.ExpectQuery(`SELECT id::text,account_id::text,owner_user_id::text,name,provider`).WithArgs("a", "box-1").
		WillReturnRows(logicalBoxRows("box-1", string(v1.LogicalBoxHibernated)))
	m.ExpectQuery(`SELECT source,COALESCE`).WithArgs("a", "box-1").WillReturnError(sql.ErrNoRows)
	// A nil provider factory would panic if the handler tried to reach the box.
	server := &Server{Store: s}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.SetPathValue("id", "box-1")
	response := httptest.NewRecorder()
	server.resyncBoxInstructions(response, request, Principal{AccountID: "a", UserID: "u", Role: "owner"})
	if response.Code != http.StatusOK {
		t.Fatalf("stopped box status=%d body=%s", response.Code, response.Body.String())
	}
	var body v1.BoxInstructionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Pending {
		t.Fatal("a stopped box must report its instructions as pending")
	}
	if !strings.Contains(body.Note, string(v1.LogicalBoxHibernated)) {
		t.Fatalf("note must name the state that blocked the re-sync: %q", body.Note)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Re-sync must never rewrite the stored snapshot: it exists to repair a running
// box that has fallen behind, not to change what the box is supposed to have.
func TestResyncBoxInstructionsIssuesNoWrites(t *testing.T) {
	s, m := testStore(t)
	m.ExpectQuery(`SELECT id::text,account_id::text,owner_user_id::text,name,provider`).WithArgs("a", "box-1").
		WillReturnRows(logicalBoxRows("box-1", string(v1.LogicalBoxHibernated)))
	m.ExpectQuery(`SELECT source,COALESCE`).WithArgs("a", "box-1").WillReturnError(sql.ErrNoRows)
	server := &Server{Store: s}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.SetPathValue("id", "box-1")
	server.resyncBoxInstructions(httptest.NewRecorder(), request, Principal{AccountID: "a", UserID: "u", Role: "owner"})
	// sqlmock fails on any statement that was not expected, and no ExpectExec
	// was registered, so a write here would already have failed the call above.
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
