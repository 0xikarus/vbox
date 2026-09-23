package controller

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestBoxMentionRequiresExactVisibleToken(t *testing.T) {
	for _, tc := range []struct {
		text, name string
		want       bool
	}{
		{"Please ask @reviewer to check", "reviewer", true},
		{"Ask @reviewer, then summarize", "reviewer", true},
		{"Ask @reviewer-extra", "reviewer", false},
		{"Email me at owner@reviewer.example", "reviewer", false},
		{"Ask reviewer without a mention", "reviewer", false},
	} {
		if got := containsBoxMention(tc.text, tc.name); got != tc.want {
			t.Fatalf("text=%q name=%q got=%v want=%v", tc.text, tc.name, got, tc.want)
		}
	}
}

func TestMentionContactsAreGrantedReciprocallyInOneTransaction(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO box_contacts").WithArgs("account-a", "source", "target", true, "user-a").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO box_contacts").WithArgs("account-a", "target", "source", true, "user-a").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO audit_log").WithArgs("account-a", "user-a", "source", "target").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := store.allowMentionContacts(context.Background(), p, "source", []string{"target"}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMentionTargetsMustBeVisibleExactBoxIDs(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"}
	mock.ExpectQuery("SELECT b.id::text,b.name,b.default_agent,b.state").WithArgs("account-a", "target-id").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "agent", "state", "protected"}).AddRow("target-id", "reviewer", "codex", "running", false))
	ids, err := store.validateMentionTargets(context.Background(), p, "source-id", "Ask @reviewer to check", []string{"target-id"})
	if err != nil || len(ids) != 1 || ids[0] != "target-id" {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	mock.ExpectQuery("SELECT b.id::text,b.name,b.default_agent,b.state").WithArgs("account-a", "target-id").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "agent", "state", "protected"}).AddRow("target-id", "reviewer", "codex", "running", true))
	if _, err := store.validateMentionTargets(context.Background(), p, "source-id", "Ask @reviewer", []string{"target-id"}); err == nil {
		t.Fatal("protected box mention was accepted")
	}
	if _, err := store.validateMentionTargets(context.Background(), Principal{Role: "agent"}, "source-id", "Ask @reviewer", []string{"target-id"}); err == nil {
		t.Fatal("agent mention was accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEffectiveAccessPrecedence(t *testing.T) {
	tests := []struct {
		name        string
		protected   bool
		override    sql.NullBool
		allContacts bool
		allowed     bool
		reasonPart  string
	}{
		{"protected beats allow", true, sql.NullBool{Valid: true, Bool: true}, true, false, "protected"},
		{"block beats all contacts", false, sql.NullBool{Valid: true, Bool: false}, true, false, "explicit"},
		{"direct contact", false, sql.NullBool{Valid: true, Bool: true}, false, true, "direct contact"},
		{"all contacts", false, sql.NullBool{}, true, true, "All contacts"},
		{"no grant", false, sql.NullBool{}, false, false, "direct contact"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allowed, reason := effectiveAccess(test.protected, test.override, test.allContacts)
			if allowed != test.allowed || !strings.Contains(reason, test.reasonPart) {
				t.Fatalf("effective access=(%t,%q), want %t containing %q", allowed, reason, test.allowed, test.reasonPart)
			}
		})
	}
}

func expectAuthorizeBase(mock sqlmock.Sqlmock, state string, protected bool, override *bool, allContacts bool) {
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM logical_boxes").WithArgs("account-a", "sender").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT b.state,EXISTS").WithArgs("account-a", "target").
		WillReturnRows(sqlmock.NewRows([]string{"state", "protected"}).AddRow(state, protected))
	overrideRows := sqlmock.NewRows([]string{"can_message"})
	if override != nil {
		overrideRows.AddRow(*override)
	}
	mock.ExpectQuery("SELECT can_message FROM box_contacts").WithArgs("account-a", "sender", "target").WillReturnRows(overrideRows)
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM box_role_assignments").WithArgs("account-a", "sender").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(allContacts))
}

func TestAuthorizeBoxMessageUsesNativeRolesAndOverrides(t *testing.T) {
	t.Run("all or selected role grant is allowed", func(t *testing.T) {
		store, mock := testStore(t)
		expectAuthorizeBase(mock, "running", false, nil, true)
		if err := store.AuthorizeBoxMessage(context.Background(), "account-a", "sender", "target"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("explicit block wins over role", func(t *testing.T) {
		store, mock := testStore(t)
		blocked := false
		expectAuthorizeBase(mock, "running", false, &blocked, true)
		if err := store.AuthorizeBoxMessage(context.Background(), "account-a", "sender", "target"); err == nil || !strings.Contains(err.Error(), "explicit") {
			t.Fatalf("block error=%v", err)
		}
	})
	t.Run("manual allow works without a role", func(t *testing.T) {
		store, mock := testStore(t)
		allowed := true
		expectAuthorizeBase(mock, "running", false, &allowed, false)
		if err := store.AuthorizeBoxMessage(context.Background(), "account-a", "sender", "target"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("protected target beats manual allow", func(t *testing.T) {
		store, mock := testStore(t)
		allowed := true
		expectAuthorizeBase(mock, "running", true, &allowed, true)
		if err := store.AuthorizeBoxMessage(context.Background(), "account-a", "sender", "target"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "protected") {
			t.Fatalf("protection error=%v", err)
		}
	})
	t.Run("authorized sleeping target is not woken", func(t *testing.T) {
		store, mock := testStore(t)
		expectAuthorizeBase(mock, "hibernated", false, nil, true)
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
		WillReturnRows(sqlmock.NewRows([]string{"box_id", "box_name", "contact_box_id", "contact_name", "default_agent", "state", "protected", "can_message", "updated_at", "all_contacts", "roles"}).
			AddRow("sender", "Sender", "target", "Target", "codex", "hibernated", false, nil, nil, true, []byte(`[{"id":"role-1","name":"Manager"}]`)))
	entries, err := store.ContactEntries(context.Background(), "account-a", "sender")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].State != "hibernated" || !strings.Contains(entries[0].Reason, "All contacts") || len(entries[0].Roles) != 1 {
		t.Fatalf("entries=%+v", entries)
	}
}

func TestShortContactIDsStayCompactAndResolvePrefixCollisions(t *testing.T) {
	views := []v1.BoxContact{
		{ContactBoxID: "abcdef12-3456-4000-8000-000000000001", CanMessage: true},
		{ContactBoxID: "abcdef12-9456-4000-8000-000000000002", CanMessage: true},
		{ContactBoxID: "12345678-3456-4000-8000-000000000003", CanMessage: true},
		{ContactBoxID: "ffffffff-ffff-4000-8000-000000000004", CanMessage: false},
	}
	got := shortContactIDs(views)
	if got[views[0].ContactBoxID] != "abcdef123" || got[views[1].ContactBoxID] != "abcdef129" {
		t.Fatalf("colliding eight-character prefixes were not safely extended: %v", got)
	}
	if got[views[2].ContactBoxID] != "123456783" {
		t.Fatalf("all visible handles should use one predictable length: %v", got)
	}
	if _, ok := got[views[3].ContactBoxID]; ok {
		t.Fatalf("an unavailable contact received a handle: %v", got)
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
