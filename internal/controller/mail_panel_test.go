package controller

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

const panelTestBox = "11111111-1111-4111-8111-111111111111"
const panelTestMail = "22222222-2222-4222-8222-222222222222"

func TestMailPanelRoutesAreRegistered(t *testing.T) {
	t.Setenv("VMBOX_MAIL_DOMAIN", "example.test")
	s := NewServer(nil, nil)
	for _, path := range []string{"/v1/mail/messages", "/v1/mail/messages/" + panelTestMail, "/v1/mail/messages/" + panelTestMail + "/attachments/" + panelTestMail, "/v1/mail/outbox", "/v1/mail/summary"} {
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("route %s returned %d", path, response.Code)
		}
	}
}

func TestMailPanelListScopesAndSearchesAcrossBoxes(t *testing.T) {
	store, mock := testStore(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "from", "from_name", "subject", "preview", "received", "unread", "attachments", "quarantined", "spf", "dkim", "box_id", "box_name", "box_address"}).
		AddRow(panelTestMail, "sender@example.test", "Sender", "Project update", "Preview", now, true, false, false, "pass", "pass", panelTestBox, "Builder", "builder@example.test")
	mock.ExpectQuery(`(?s)FROM mail_messages m JOIN logical_boxes b.*WHERE m.account_id=\$1.*m.box_id::text=\$2.*\$3='unread'.*position\(lower\(\$4\).*ORDER BY m.received_at DESC`).
		WithArgs("account-a", panelTestBox, "unread", "project", nil, "").WillReturnRows(rows)
	s := &Server{Store: store}
	r := httptest.NewRequest(http.MethodGet, "/v1/mail/messages?box="+panelTestBox+"&folder=unread&q=project", nil)
	w := httptest.NewRecorder()
	s.ownerMailPanelMessages(w, r, Principal{AccountID: "account-a", Role: "owner"})
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Messages   []mailPanelMessageRow `json:"messages"`
		NextCursor string                `json:"nextCursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 1 || body.Messages[0].ID != panelTestMail || body.Messages[0].BoxID != panelTestBox || body.Messages[0].BoxAddress != "builder@example.test" {
		t.Fatalf("wrong list: %+v", body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMailPanelOtherAccountDetailIsHidden(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`SELECT m.box_id::text,b.name,COALESCE\(ms.address,''\).*WHERE m.account_id=\$1 AND m.id=\$2`).
		WithArgs("account-a", panelTestMail).WillReturnError(sql.ErrNoRows)
	s := &Server{Store: store}
	r := httptest.NewRequest(http.MethodGet, "/v1/mail/messages/"+panelTestMail, nil)
	r.SetPathValue("mid", panelTestMail)
	w := httptest.NewRecorder()
	s.ownerMailPanelMessage(w, r, Principal{AccountID: "account-a", Role: "owner"})
	if w.Code != 404 {
		t.Fatalf("foreign message status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMailPanelQuarantineFilterAndInvalidParameters(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`(?s)FROM mail_messages m.*m.account_id=\$1.*\$3='quarantine'.*m.quarantined`).
		WithArgs("account-a", "", "quarantine", "", nil, "").WillReturnRows(sqlmock.NewRows([]string{"id", "from", "from_name", "subject", "preview", "received", "unread", "attachments", "quarantined", "spf", "dkim", "box_id", "box_name", "box_address"}))
	s := &Server{Store: store}
	w := httptest.NewRecorder()
	s.ownerMailPanelMessages(w, httptest.NewRequest(http.MethodGet, "/v1/mail/messages?folder=quarantine", nil), Principal{AccountID: "account-a"})
	if w.Code != 200 {
		t.Fatalf("quarantine status=%d body=%s", w.Code, w.Body.String())
	}
	for _, target := range []string{"/v1/mail/messages?folder=other", "/v1/mail/messages?box=not-a-uuid", "/v1/mail/messages?q=" + strings.Repeat("x", 201)} {
		w := httptest.NewRecorder()
		s.ownerMailPanelMessages(w, httptest.NewRequest(http.MethodGet, target, nil), Principal{AccountID: "account-a"})
		if w.Code != 400 {
			t.Fatalf("invalid %s status=%d", target, w.Code)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMailPanelOutboxFiltersAndSummaryTotals(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`(?s)FROM mail_outbox o JOIN logical_boxes b.*WHERE o.account_id=\$1.*o.box_id::text=\$2.*o.status=\$3`).
		WithArgs("account-a", panelTestBox, "pending_approval", nil, "").WillReturnRows(sqlmock.NewRows([]string{"id", "recipients", "subject", "text", "status", "reason", "version", "created", "decided", "sent", "box", "name", "address"}))
	s := &Server{Store: store}
	w := httptest.NewRecorder()
	s.ownerMailPanelOutbox(w, httptest.NewRequest(http.MethodGet, "/v1/mail/outbox?box="+panelTestBox+"&status=pending_approval", nil), Principal{AccountID: "account-a"})
	if w.Code != 200 {
		t.Fatalf("outbox status=%d body=%s", w.Code, w.Body.String())
	}
	mock.ExpectQuery(`(?s)FROM logical_boxes b LEFT JOIN box_mail_settings ms.*WHERE b.account_id=\$1`).WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "address", "enabled", "unread", "quarantine"}).AddRow(panelTestBox, "Builder", "builder@example.test", true, 3, 2))
	mock.ExpectQuery(`SELECT count\(\*\) FROM mail_outbox WHERE account_id=\$1`).WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"pending"}).AddRow(1))
	w = httptest.NewRecorder()
	s.ownerMailPanelSummary(w, httptest.NewRequest(http.MethodGet, "/v1/mail/summary", nil), Principal{AccountID: "account-a"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"unread":3`) || !strings.Contains(w.Body.String(), `"quarantine":2`) || !strings.Contains(w.Body.String(), `"pending":1`) {
		t.Fatalf("summary status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
