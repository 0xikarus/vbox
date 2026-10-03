package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestDefaultProviderResolvesOnlyUnambiguousCredential(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured [][2]string
		wantStatus int
		wantBody   string
	}{
		{"one provider", [][2]string{{"shared-worker", "primary"}}, http.StatusOK, `"inferred":true`},
		{"no provider", nil, http.StatusConflict, "Choose a default provider in Providers"},
		{"two providers", [][2]string{{"railway", "cloud"}, {"shared-worker", "primary"}}, http.StatusConflict, "Choose a default provider in Providers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := testStore(t)
			mock.ExpectQuery(`SELECT provider,provider_credential FROM controller_defaults`).WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"provider", "provider_credential"}))
			rows := sqlmock.NewRows([]string{"provider", "name"})
			for _, item := range tc.configured {
				rows.AddRow(item[0], item[1])
			}
			mock.ExpectQuery(`SELECT provider,name FROM provider_credentials WHERE account_id=\$1 AND deleting=false`).WithArgs("account-a").WillReturnRows(rows)
			w := httptest.NewRecorder()
			NewServer(store, provider.NewRegistry()).defaultProviderHandler(w, httptest.NewRequest(http.MethodGet, "/v1/controller-defaults", nil), Principal{AccountID: "account-a", Role: "owner"})
			if w.Code != tc.wantStatus || !strings.Contains(w.Body.String(), tc.wantBody) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.name == "one provider" && (!strings.Contains(w.Body.String(), `"provider":"shared-worker"`) || !strings.Contains(w.Body.String(), `"providerCredential":"primary"`)) {
				t.Fatalf("single provider was not resolved: %s", w.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDefaultProviderPrefersSavedChoice(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`SELECT provider,provider_credential FROM controller_defaults`).WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"provider", "provider_credential"}).AddRow("railway", "cloud"))
	w := httptest.NewRecorder()
	NewServer(store, provider.NewRegistry()).defaultProviderHandler(w, httptest.NewRequest(http.MethodGet, "/v1/controller-defaults", nil), Principal{AccountID: "account-a", Role: "owner"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"provider":"railway","providerCredential":"cloud"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
