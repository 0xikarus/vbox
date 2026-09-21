package controller

import (
	"context"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func contactBoxRows(id, name string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "name", "role", "default_agent", "state", "protected"}).
		AddRow(id, name, "worker", "claude", "running", false)
}

func TestPutBoxContactCreatesBothDirections(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"}
	mock.ExpectQuery(`SELECT id::text,account_id::text,owner_user_id::text,name,provider`).WithArgs("account-a", "builder").
		WillReturnRows(logicalBoxRows("builder-id", "running"))
	mock.ExpectQuery(`SELECT b.id::text,b.name`).WithArgs("account-a", "reviewer").
		WillReturnRows(contactBoxRows("reviewer-id", "reviewer"))
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO box_contacts`).
		WithArgs("account-a", "builder-id", "reviewer-id", nil, nil, "user-a").
		WillReturnRows(sqlmock.NewRows([]string{"box_id", "contact_box_id", "can_message", "can_receive", "updated_at"}).
			AddRow("builder-id", "reviewer-id", true, true, time.Now().UTC()))
	mock.ExpectExec(`INSERT INTO box_contacts`).
		WithArgs("account-a", "builder-id", "reviewer-id", nil, nil, "user-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO audit_log`).
		WithArgs("account-a", "user-a", "builder-id", "reviewer-id", "reviewer", false).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if _, err := store.PutBoxContact(context.Background(), p, "builder", v1.PutBoxContactRequest{Contact: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteBoxContactRemovesBothDirections(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"}
	mock.ExpectQuery(`SELECT id::text,account_id::text,owner_user_id::text,name,provider`).WithArgs("account-a", "builder").
		WillReturnRows(logicalBoxRows("builder-id", "running"))
	mock.ExpectQuery(`SELECT b.id::text,b.name`).WithArgs("account-a", "reviewer").
		WillReturnRows(contactBoxRows("reviewer-id", "reviewer"))
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM box_contacts`).WithArgs("account-a", "builder-id", "reviewer-id").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`INSERT INTO audit_log`).WithArgs("account-a", "user-a", "builder-id", "reviewer-id").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := store.DeleteBoxContact(context.Background(), p, "builder", "reviewer"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func contactCandidateRows(rows ...string) *sqlmock.Rows {
	result := sqlmock.NewRows([]string{"id", "name", "role", "default_agent", "state"})
	for _, name := range rows {
		result.AddRow(name, name, "worker", "claude", "running")
	}
	return result
}

func contactEdgeRows(id string, canMessage, canReceive bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"contact_box_id", "can_message", "can_receive"}).AddRow(id, canMessage, canReceive)
}

func TestContactEntriesWorkerSeesOnlyExplicitEdges(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("SELECT COALESCE\\(role").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("worker"))
	mock.ExpectQuery("FROM box_contacts").WithArgs("account-a", "box-a").
		WillReturnRows(contactEdgeRows("builder", true, true))
	mock.ExpectQuery("FROM logical_boxes b").WithArgs("account-a", "box-a").
		WillReturnRows(contactCandidateRows("builder", "stranger"))
	entries, err := store.ContactEntries(context.Background(), "account-a", "box-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "builder" || !entries[0].CanMessage {
		t.Fatalf("worker contacts=%+v", entries)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestContactEntriesManagerSeesEveryNonProtectedBox(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("SELECT COALESCE\\(role").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("manager"))
	mock.ExpectQuery("FROM box_contacts").WithArgs("account-a", "box-a").
		WillReturnRows(contactEdgeRows("blocked", false, true))
	mock.ExpectQuery("FROM logical_boxes b").WithArgs("account-a", "box-a").
		WillReturnRows(contactCandidateRows("open", "blocked"))
	entries, err := store.ContactEntries(context.Background(), "account-a", "box-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "open" || !entries[0].CanMessage {
		t.Fatalf("manager contacts=%+v", entries)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeBoxMessageEnforcesManagerAndWorkerRules(t *testing.T) {
	t.Run("manager without an explicit edge is allowed", func(t *testing.T) {
		store, mock := testStore(t)
		mock.ExpectQuery("SELECT COALESCE\\(role").WithArgs("account-a", "sender").
			WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("manager"))
		mock.ExpectQuery("SELECT state FROM logical_boxes").WithArgs("account-a", "target").
			WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("running"))
		mock.ExpectQuery("SELECT EXISTS").WithArgs("account-a", "target").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectQuery("SELECT can_message FROM box_contacts").WithArgs("account-a", "sender", "target").
			WillReturnRows(sqlmock.NewRows([]string{"can_message"}))
		if err := store.AuthorizeBoxMessage(context.Background(), "account-a", "sender", "target"); err != nil {
			t.Fatalf("manager edge rejected: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("worker without an edge is rejected", func(t *testing.T) {
		store, mock := testStore(t)
		mock.ExpectQuery("SELECT COALESCE\\(role").WithArgs("account-a", "sender").
			WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("worker"))
		mock.ExpectQuery("SELECT state FROM logical_boxes").WithArgs("account-a", "target").
			WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("running"))
		mock.ExpectQuery("SELECT EXISTS").WithArgs("account-a", "target").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectQuery("SELECT can_message FROM box_contacts").WithArgs("account-a", "sender", "target").
			WillReturnRows(sqlmock.NewRows([]string{"can_message"}))
		if err := store.AuthorizeBoxMessage(context.Background(), "account-a", "sender", "target"); err == nil {
			t.Fatal("worker without an edge was accepted")
		}
	})
	t.Run("protected target is rejected even for a manager", func(t *testing.T) {
		store, mock := testStore(t)
		mock.ExpectQuery("SELECT COALESCE\\(role").WithArgs("account-a", "sender").
			WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("manager"))
		mock.ExpectQuery("SELECT state FROM logical_boxes").WithArgs("account-a", "target").
			WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("running"))
		mock.ExpectQuery("SELECT EXISTS").WithArgs("account-a", "target").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		if err := store.AuthorizeBoxMessage(context.Background(), "account-a", "sender", "target"); err == nil {
			t.Fatal("protected target was accepted")
		}
	})
	t.Run("a sleeping target is rejected without provisioning", func(t *testing.T) {
		store, mock := testStore(t)
		mock.ExpectQuery("SELECT COALESCE\\(role").WithArgs("account-a", "sender").
			WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("manager"))
		mock.ExpectQuery("SELECT state FROM logical_boxes").WithArgs("account-a", "target").
			WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("hibernated"))
		if err := store.AuthorizeBoxMessage(context.Background(), "account-a", "sender", "target"); err == nil {
			t.Fatal("hibernated target was accepted")
		}
	})
}

func TestBoxMessageOriginMarksInterBoxTraffic(t *testing.T) {
	if direction, sender := boxMessageOrigin(""); direction != "user" || sender != nil {
		t.Fatalf("owner message origin=%q %v", direction, sender)
	}
	if direction, sender := boxMessageOrigin("box-a"); direction != "box" || sender != "box-a" {
		t.Fatalf("contact message origin=%q %v", direction, sender)
	}
}
