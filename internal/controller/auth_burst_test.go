package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/secrets"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestAuthenticatedPollingDoesNotExhaustAccountBudget(t *testing.T) {
	store, mock := testStore(t)
	s := NewServer(store, nil)
	called := 0
	h := s.owner(func(w http.ResponseWriter, _ *http.Request, _ Principal) {
		called++
		w.WriteHeader(http.StatusNoContent)
	})
	for i := 0; i < 250; i++ {
		mock.ExpectQuery(`SELECT t.account_id`).WithArgs(secrets.TokenHash("test-token")).WillReturnRows(
			sqlmock.NewRows([]string{"account_id", "user_id", "role", "subject"}).AddRow("account", "user", "owner", "subject"))
		r := httptest.NewRequest("GET", "/v1/whoami", nil)
		r.Header.Set("Authorization", "Bearer test-token")
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("request %d: status %d", i+1, w.Code)
		}
	}
	// Removing the traffic quota must not bypass authentication or owner checks.
	for _, tc := range []struct {
		role   string
		status int
	}{{"", 401}, {"user", 403}} {
		rows := sqlmock.NewRows([]string{"account_id", "user_id", "role", "subject"})
		if tc.role != "" {
			rows.AddRow("account", "user", tc.role, "subject")
		}
		mock.ExpectQuery(`SELECT t.account_id`).WithArgs(secrets.TokenHash("test-token")).WillReturnRows(rows)
		r := httptest.NewRequest("GET", "/v1/whoami", nil)
		r.Header.Set("Authorization", "Bearer test-token")
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != tc.status {
			t.Fatalf("role %q: status %d", tc.role, w.Code)
		}
	}
	if called != 250 {
		t.Fatalf("handler called %d times", called)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
