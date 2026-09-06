package controller

import (
	"encoding/json"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	"github.com/DATA-DOG/go-sqlmock"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeleteProfileAccountScoped(t *testing.T) {
	for _, count := range []int64{0, 1} {
		s, m := testStore(t)
		m.ExpectExec("DELETE FROM login_profiles WHERE account_id").WithArgs("a", "claude", "personal").WillReturnResult(sqlmock.NewResult(0, count))
		r := httptest.NewRequest("DELETE", "/", nil)
		r.SetPathValue("application", "claude")
		r.SetPathValue("name", "personal")
		w := httptest.NewRecorder()
		(&Server{Store: s}).deleteLoginProfile(w, r, Principal{AccountID: "a"})
		expected := 204
		if count == 0 {
			expected = 404
		}
		if w.Code != expected {
			t.Fatal(w.Code)
		}
		if err := m.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRotateCurrentTokenOnly(t *testing.T) {
	s, m := testStore(t)
	hash := secrets.TokenHash("new-test-token")
	m.ExpectExec("UPDATE access_tokens SET token_hash").WithArgs(hash, "a", "u", secrets.TokenHash("old-test-token")).WillReturnResult(sqlmock.NewResult(0, 1))
	data, _ := json.Marshal(map[string]any{"hash": hash})
	r := httptest.NewRequest("POST", "/", strings.NewReader(string(data)))
	r.Header.Set("Authorization", "Bearer old-test-token")
	w := httptest.NewRecorder()
	(&Server{Store: s}).rotateToken(w, r, Principal{AccountID: "a", UserID: "u"})
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
