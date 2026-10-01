package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestChatReadMarkersValidate(t *testing.T) {
	for _, body := range []string{
		`{"group:one":"2026-01-01T00:00:00Z"}`,
		`{"pair:one":"2026-01-01T00:00:00Z"}`,
		`{"box:bad/path":"2026-01-01T00:00:00Z"}`,
		`{"box:one":"not a date"}`,
		`{"box:one":"3000-01-01T00:00:00Z"}`,
	} {
		store, mock := testStore(t)
		w := httptest.NewRecorder()
		(&Server{Store: store}).chatReadMarkersHandler(w, httptest.NewRequest(http.MethodPut, "/v1/chat-read-markers", strings.NewReader(body)), Principal{AccountID: "account-a"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d", body, w.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestChatReadMarkersAdvanceAndReadByAccount(t *testing.T) {
	store, mock := testStore(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO chat_read_markers").WithArgs("account-a", "box:one", at).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT chat_key,seen_at FROM chat_read_markers").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"chat_key", "seen_at"}).AddRow("box:one", at))
	w := httptest.NewRecorder()
	(&Server{Store: store}).chatReadMarkersHandler(w, httptest.NewRequest(http.MethodPut, "/v1/chat-read-markers", strings.NewReader(`{"box:one":"2026-01-01T00:00:00Z"}`)), Principal{AccountID: "account-a"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"box:one":"2026-01-01T00:00:00Z"`) {
		t.Fatalf("PUT status=%d body=%s", w.Code, w.Body.String())
	}
	mock.ExpectQuery("SELECT chat_key,seen_at FROM chat_read_markers").WithArgs("account-b").WillReturnRows(sqlmock.NewRows([]string{"chat_key", "seen_at"}))
	w = httptest.NewRecorder()
	(&Server{Store: store}).chatReadMarkersHandler(w, httptest.NewRequest(http.MethodGet, "/v1/chat-read-markers", nil), Principal{AccountID: "account-b"})
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{}` {
		t.Fatalf("GET status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChatReadMarkersStaleWriteKeepsNewerTimestamp(t *testing.T) {
	store, mock := testStore(t)
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	mock.ExpectBegin()
	mock.ExpectExec("ON CONFLICT.*GREATEST").WithArgs("account-a", "box:one", older).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT chat_key,seen_at FROM chat_read_markers").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"chat_key", "seen_at"}).AddRow("box:one", newer))
	w := httptest.NewRecorder()
	(&Server{Store: store}).chatReadMarkersHandler(w, httptest.NewRequest(http.MethodPut, "/v1/chat-read-markers", strings.NewReader(`{"box:one":"2026-01-01T00:00:00Z"}`)), Principal{AccountID: "account-a"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"box:one":"2026-01-01T01:00:00Z"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
