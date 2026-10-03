package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestApprovedMailUsesResendAPIWithStableIdempotency(t *testing.T) {
	t.Setenv("VMBOX_RESEND_API_KEY", "synthetic-test-key")
	var calls int
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/emails" || r.Header.Get("Authorization") != "Bearer synthetic-test-key" || r.Header.Get("Idempotency-Key") != "draft-1" {
			t.Errorf("unexpected Resend request: method=%s path=%s", r.Method, r.URL.Path)
		}
		var body struct {
			From    string   `json:"from"`
			To      []string `json:"to"`
			Subject string   `json:"subject"`
			Text    string   `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.From != "box@example.test" || !reflect.DeepEqual(body.To, []string{"recipient@example.test"}) || body.Subject != "Approved" || body.Text != "Reviewed text" {
			t.Errorf("wrong approved mail body: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resend-123"}`))
	}))
	defer remote.Close()
	s := &Server{ResendURL: remote.URL + "/emails", HTTP: remote.Client()}
	id, err := s.sendApprovedMail(context.Background(), "draft-1", "box@example.test", []string{"recipient@example.test"}, "Approved", "Reviewed text")
	if err != nil || id != "resend-123" || calls != 1 {
		t.Fatalf("Resend result id=%q err=%v calls=%d", id, err, calls)
	}
}

func TestStaleApprovedMailRetriesWithStableIdempotency(t *testing.T) {
	t.Setenv("VMBOX_MAIL_DOMAIN", "example.test")
	t.Setenv("VMBOX_RESEND_API_KEY", "synthetic-test-key")
	var calls int
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Idempotency-Key") != "draft-1" {
			t.Errorf("wrong idempotency key")
		}
		_, _ = w.Write([]byte(`{"id":"resend-123"}`))
	}))
	defer remote.Close()
	store, mock := testStore(t)
	mock.ExpectQuery("SELECT o.account_id::text,o.box_id::text,o.id::text").WillReturnRows(sqlmock.NewRows([]string{"account", "box", "id", "address", "recipients", "subject", "text"}).AddRow("account-a", "box-a", "draft-1", "box@example.test", []byte(`["recipient@example.test"]`), "Approved", "Reviewed text"))
	mock.ExpectExec("UPDATE mail_outbox SET updated_at=now\\(\\)").WithArgs("account-a", "box-a", "draft-1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE mail_outbox SET status=\\$4").WithArgs("account-a", "box-a", "draft-1", "sent", "", "resend-123").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO mail_notice_queue").WillReturnResult(sqlmock.NewResult(0, 1))
	s := &Server{Store: store, ResendURL: remote.URL + "/emails", HTTP: remote.Client()}
	if err := s.ReconcileStaleMailSendsNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("Resend calls=%d", calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
