package controller

import (
	"github.com/DATA-DOG/go-sqlmock"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWhoamiUsesAuthenticatedAccount(t *testing.T) {
	s, mock := testStore(t)
	mock.ExpectQuery(`SELECT name FROM accounts WHERE id=\$1`).WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("team"))
	w := httptest.NewRecorder()
	(&Server{Store: s}).whoami(w, httptest.NewRequest(http.MethodGet, "/v1/whoami?accountId=other", nil), Principal{AccountID: "account-a", UserID: "alice-id", Subject: "alice", Role: "user"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"accountId":"account-a"`) || !strings.Contains(w.Body.String(), `"role":"user"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
