package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestImportedCredentialsReadsControllerStateWithoutWorker(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  error
		status int
	}{
		{name: "recorded profiles", status: http.StatusOK},
		{name: "database unavailable", state: errors.New("database unavailable"), status: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := testStore(t)
			mock.ExpectQuery(`SELECT id::text,account_id::text,owner_user_id::text,name,provider`).WithArgs("a", "box-1").
				WillReturnRows(logicalBoxRows("box-1", "hibernated"))
			query := mock.ExpectQuery(`SELECT COALESCE\(metadata->'importedLoginProfiles'`).WithArgs("a", "box-1")
			if tc.state != nil {
				query.WillReturnError(tc.state)
			} else {
				query.WillReturnRows(sqlmock.NewRows([]string{"imported", "verified", "pending"}).
					AddRow(`[{"application":"claude","name":"work"}]`, true, `[]`))
			}
			server := &Server{Store: store} // A nil provider would panic if this GET contacted a worker.
			request := httptest.NewRequest(http.MethodGet, "/v1/logical-boxes/box-1/imported-credentials", nil)
			request.SetPathValue("id", "box-1")
			response := httptest.NewRecorder()
			server.importedCredentials(response, request, Principal{AccountID: "a", UserID: "u", Role: "owner"})
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.state == nil && !strings.Contains(response.Body.String(), `"name":"work"`) {
				t.Fatalf("missing recorded profile: %s", response.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
