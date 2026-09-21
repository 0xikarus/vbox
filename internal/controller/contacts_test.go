package controller

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestEffectiveAccessPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		protected  bool
		override   sql.NullBool
		role       string
		allowed    bool
		reasonPart string
	}{
		{"protected beats allow", true, sql.NullBool{Valid: true, Bool: true}, "All contacts", false, "protected"},
		{"block beats role", false, sql.NullBool{Valid: true, Bool: false}, "All contacts", false, "explicit"},
		{"manual allow", false, sql.NullBool{Valid: true, Bool: true}, "", true, "explicit"},
		{"role grant", false, sql.NullBool{}, "Selected contacts", true, "Selected contacts"},
		{"no grant", false, sql.NullBool{}, "", false, "No role"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allowed, reason := effectiveAccess(test.protected, test.override, test.role)
			if allowed != test.allowed || !strings.Contains(reason, test.reasonPart) {
				t.Fatalf("effective access=(%t,%q), want %t containing %q", allowed, reason, test.allowed, test.reasonPart)
			}
		})
	}
}

func expectAuthorizeBase(mock sqlmock.Sqlmock, state string, protected bool, override *bool, role string) {
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM logical_boxes").WithArgs("account-a", "sender").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT b.state,EXISTS").WithArgs("account-a", "target").
		WillReturnRows(sqlmock.NewRows([]string{"state", "protected"}).AddRow(state, protected))
	overrideRows := sqlmock.NewRows([]string{"can_message"})
	if override != nil {
		overrideRows.AddRow(*override)
	}
	mock.ExpectQuery("SELECT can_message FROM box_contacts").WithArgs("account-a", "sender", "target").WillReturnRows(overrideRows)
	roleRows := sqlmock.NewRows([]string{"name"})
	if role != "" {
		roleRows.AddRow(role)
	}
	mock.ExpectQuery("SELECT r.name FROM box_role_assignments").WithArgs("account-a", "sender", "target").WillReturnRows(roleRows)
}

func TestAuthorizeBoxMessageUsesNativeRolesAndOverrides(t *testing.T) {
	t.Run("all or selected role grant is allowed", func(t *testing.T) {
		store, mock := testStore(t)
		expectAuthorizeBase(mock, "running", false, nil, "All contacts")
		if err := store.AuthorizeBoxMessage(context.Background(), "account-a", "sender", "target"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("explicit block wins over role", func(t *testing.T) {
		store, mock := testStore(t)
		blocked := false
		expectAuthorizeBase(mock, "running", false, &blocked, "All contacts")
		if err := store.AuthorizeBoxMessage(context.Background(), "account-a", "sender", "target"); err == nil || !strings.Contains(err.Error(), "explicit") {
			t.Fatalf("block error=%v", err)
		}
	})
	t.Run("manual allow works without a role", func(t *testing.T) {
		store, mock := testStore(t)
		allowed := true
		expectAuthorizeBase(mock, "running", false, &allowed, "")
		if err := store.AuthorizeBoxMessage(context.Background(), "account-a", "sender", "target"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("protected target beats manual allow", func(t *testing.T) {
		store, mock := testStore(t)
		allowed := true
		expectAuthorizeBase(mock, "running", true, &allowed, "All contacts")
		if err := store.AuthorizeBoxMessage(context.Background(), "account-a", "sender", "target"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "protected") {
			t.Fatalf("protection error=%v", err)
		}
	})
	t.Run("authorized sleeping target is not woken", func(t *testing.T) {
		store, mock := testStore(t)
		expectAuthorizeBase(mock, "hibernated", false, nil, "All contacts")
		if err := store.AuthorizeBoxMessage(context.Background(), "account-a", "sender", "target"); err == nil || !strings.Contains(err.Error(), "hibernated") {
			t.Fatalf("readiness error=%v", err)
		}
	})
}

func TestContactEntriesIncludesAuthorizedSleepingContactAndExplanation(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("SELECT name FROM logical_boxes").WithArgs("account-a", "sender").
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("Sender"))
	mock.ExpectQuery("SELECT \\(\\$2::uuid\\)::text,\\$3::text").WithArgs("account-a", "sender", "Sender").
		WillReturnRows(sqlmock.NewRows([]string{"box_id", "box_name", "contact_box_id", "contact_name", "default_agent", "state", "protected", "can_message", "updated_at", "role_name", "roles"}).
			AddRow("sender", "Sender", "target", "Target", "codex", "hibernated", false, nil, nil, "Selected contacts", []byte(`[{"id":"role-1","name":"Selected contacts"}]`)))
	entries, err := store.ContactEntries(context.Background(), "account-a", "sender")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].State != "hibernated" || !strings.Contains(entries[0].Reason, "Selected contacts") || len(entries[0].Roles) != 1 {
		t.Fatalf("entries=%+v", entries)
	}
}

func TestBoxMessageOriginMarksInterBoxTraffic(t *testing.T) {
	if direction, sender := boxMessageOrigin(""); direction != "user" || sender != nil {
		t.Fatalf("owner message origin=%q %v", direction, sender)
	}
	if direction, sender := boxMessageOrigin("box-a"); direction != "box" || sender != "box-a" {
		t.Fatalf("contact message origin=%q %v", direction, sender)
	}
}
