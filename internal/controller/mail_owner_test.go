package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMailOwnerRoutesAreUnavailableWithoutDomain(t *testing.T) {
	t.Setenv("VMBOX_MAIL_DOMAIN", "")
	s := &Server{}
	for _, path := range []string{"/v1/mail/approvals", "/v1/mail/messages", "/v1/mail/messages/message-id", "/v1/mail/outbox", "/v1/mail/summary", "/v1/mail/addresses", "/v1/mail/addresses/address-id", "/v1/mail/settings", "/v1/logical-boxes/box-1/mail", "/v1/logical-boxes/box-1/mail/messages", "/v1/logical-boxes/box-1/mail/outbox/draft-1"} {
		t.Run(path, func(t *testing.T) {
			called := false
			wrapped := s.mailConfigured(func(http.ResponseWriter, *http.Request, Principal) { called = true })
			response := httptest.NewRecorder()
			wrapped(response, httptest.NewRequest(http.MethodGet, path, nil), Principal{Role: "owner"})
			if called || response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "mail is not configured") {
				t.Fatalf("feature-off response=%d %q; backend called=%t", response.Code, response.Body.String(), called)
			}
		})
	}
}

func TestOwnerMailOutboxItemGETIsRegistered(t *testing.T) {
	t.Setenv("VMBOX_MAIL_DOMAIN", "example.test")
	response := httptest.NewRecorder()
	NewServer(nil, nil).Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/logical-boxes/box-1/mail/outbox/draft-1", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("GET outbox item route is not registered: %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	NewServer(nil, nil).Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/logical-boxes/"+panelTestBox+"/mail/messages/"+panelTestMail+"/archive", strings.NewReader(`{"archived":true}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("archive route is not registered: %d %s", response.Code, response.Body.String())
	}
}

func TestOwnerMailArchiveAndUnarchiveStayWithinBox(t *testing.T) {
	store, mock := testStore(t)
	s := &Server{Store: store}
	for _, archived := range []bool{true, false} {
		mock.ExpectQuery(`SELECT id::text,account_id::text,owner_user_id::text,name,provider`).WithArgs("a", panelTestBox).WillReturnRows(logicalBoxRows(panelTestBox, "running"))
		mock.ExpectQuery(`UPDATE mail_messages SET archived_at=CASE WHEN \$4::bool THEN now\(\) ELSE NULL END WHERE account_id=\$1 AND box_id=\$2 AND id=\$3.*NOT quarantined RETURNING id::text`).
			WithArgs("a", panelTestBox, panelTestMail, archived).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(panelTestMail))
		body := `{"archived":false}`
		if archived {
			body = `{"archived":true}`
		}
		r := httptest.NewRequest(http.MethodPost, "/v1/logical-boxes/"+panelTestBox+"/mail/messages/"+panelTestMail+"/archive", strings.NewReader(body))
		r.SetPathValue("id", panelTestBox)
		r.SetPathValue("mid", panelTestMail)
		w := httptest.NewRecorder()
		s.ownerMailArchive(w, r, Principal{AccountID: "a", Role: "owner"})
		if w.Code != 200 {
			t.Fatalf("archive=%t status=%d body=%s", archived, w.Code, w.Body.String())
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerMailArchiveFolderFilters(t *testing.T) {
	store, mock := testStore(t)
	s := &Server{Store: store}
	for _, folder := range []string{"all", "unread", "archive"} {
		mock.ExpectQuery(`SELECT id::text,account_id::text,owner_user_id::text,name,provider`).WithArgs("a", panelTestBox).WillReturnRows(logicalBoxRows(panelTestBox, "running"))
		mock.ExpectQuery(`(?s)FROM mail_messages WHERE account_id=\$1 AND box_id=\$2.*\$3='all'.*archived_at IS NULL.*\$3='unread'.*archived_at IS NULL.*\$3='archive'.*archived_at IS NOT NULL`).
			WithArgs("a", panelTestBox, folder, nil, "").WillReturnRows(sqlmock.NewRows([]string{"id"}))
		r := httptest.NewRequest(http.MethodGet, "/v1/logical-boxes/"+panelTestBox+"/mail/messages?folder="+folder, nil)
		r.SetPathValue("id", panelTestBox)
		w := httptest.NewRecorder()
		s.ownerMailMessages(w, r, Principal{AccountID: "a", Role: "owner"})
		if w.Code != 200 {
			t.Fatalf("folder=%s status=%d body=%s", folder, w.Code, w.Body.String())
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
