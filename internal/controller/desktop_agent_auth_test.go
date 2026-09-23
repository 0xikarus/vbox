package controller

import (
	"crypto/sha256"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestDesktopAgentAuthUsesDatabaseBoxAndRejectsExpiredAssignment(t *testing.T) {
	for _, valid := range []bool{true, false} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		token := strings.Repeat("a", 64)
		hash := sha256.Sum256([]byte(token))
		query := mock.ExpectQuery(`SELECT t.account_id::text,t.user_id::text,t.box_id::text,t.fencing_token FROM desktop_agent_tokens`).WithArgs(hash[:], "/")
		if valid {
			query.WillReturnRows(sqlmock.NewRows([]string{"account", "user", "box", "fence"}).AddRow("account", "user", "bound-box", "current-fence"))
		} else {
			query.WillReturnError(sql.ErrNoRows)
		}
		server := NewServer(&Store{DB: db}, nil)
		called := false
		handler := server.desktopAgentAuth(func(w http.ResponseWriter, r *http.Request, p Principal) {
			called = true
			scope, ok := r.Context().Value(desktopAgentScopeKey{}).(desktopAgentScope)
			if !ok || scope.Fence != "current-fence" || scope.TokenHash != hash {
				t.Error("authenticated assignment not retained")
			}
			if r.PathValue("id") != "bound-box" || p.Role != "desktop-agent" || p.AccountID != "account" {
				t.Error("caller identity escaped credential scope")
			}
			w.WriteHeader(204)
		})
		r := httptest.NewRequest("POST", "/", nil)
		r.SetPathValue("id", "attacker-chosen-box")
		r.Header.Set("Authorization", "DesktopAgent "+token)
		w := httptest.NewRecorder()
		handler(w, r)
		if called != valid || (!valid && w.Code != 401) {
			t.Fatal("authorization result incorrect")
		}
		if strings.Contains(w.Body.String(), token) {
			t.Fatal("credential echoed")
		}
		if err = mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
}
