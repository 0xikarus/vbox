package controller

import (
	"crypto/sha256"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	"github.com/DATA-DOG/go-sqlmock"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBrowserSessionLifecycleAndOrigin(t *testing.T) {
	s := &Server{PublicURL: "https://controller.example"}
	login := httptest.NewRequest("POST", "https://controller.example/v1/browser-session", nil)
	login.Header.Set("Authorization", "Bearer synthetic-secret")
	login.Header.Set("Origin", s.PublicURL)
	w := httptest.NewRecorder()
	s.browserLogin(w, login, Principal{})
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Value == "synthetic-secret" {
		t.Fatal("unsafe cookie")
	}
	for _, test := range []struct {
		method, origin, site string
		allowed              bool
	}{
		{"GET", "", "same-origin", true},
		{"POST", s.PublicURL, "same-origin", true},
		{"POST", "", "", false},
		{"POST", "https://attacker.example", "", false},
		{"GET", "", "cross-site", false},
	} {
		r := httptest.NewRequest(test.method, s.PublicURL+"/v1/whoami", nil)
		r.AddCookie(cookie)
		r.Header.Set("Origin", test.origin)
		r.Header.Set("Sec-Fetch-Site", test.site)
		token, ok := s.browserToken(r)
		if ok != test.allowed || ok && token != "synthetic-secret" {
			t.Fatal(test, ok)
		}
	}
	r := httptest.NewRequest("DELETE", s.PublicURL+"/v1/browser-session", nil)
	r.Header.Set("Origin", s.PublicURL)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.browserLogout(w, r)
	if w.Code != 204 || len(s.browserSessions) != 0 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout failed")
	}
	s.browserSessions[sha256.Sum256([]byte(cookie.Value))] = browserSession{token: "expired", expires: time.Now().Add(-time.Second)}
	if _, ok := s.browserToken(r); ok {
		t.Fatal("expired cookie accepted")
	}
}

func TestBrowserCookieRechecksTokenAndRole(t *testing.T) {
	store, mock := testStore(t)
	s := &Server{Store: store, PublicURL: "https://controller.example", browserSessions: map[[32]byte]browserSession{
		sha256.Sum256([]byte("opaque-cookie")): {token: "original-token", expires: time.Now().Add(time.Hour)},
	}}
	for _, test := range []struct {
		role    string
		revoked bool
		status  int
	}{{"owner", false, 204}, {"user", false, 403}, {"owner", true, 401}} {
		rows := sqlmock.NewRows([]string{"account_id", "user_id", "role", "subject"})
		if !test.revoked {
			rows.AddRow("account", "user", test.role, "subject")
		}
		mock.ExpectQuery(`SELECT t.account_id`).WithArgs(secrets.TokenHash("original-token")).WillReturnRows(rows)
		r := httptest.NewRequest("GET", s.PublicURL+"/v1/test", nil)
		r.AddCookie(&http.Cookie{Name: browserCookie, Value: "opaque-cookie"})
		w := httptest.NewRecorder()
		s.owner(func(w http.ResponseWriter, _ *http.Request, p Principal) {
			if p.AccountID != "account" {
				t.Fatal("wrong account")
			}
			w.WriteHeader(204)
		})(w, r)
		if w.Code != test.status {
			t.Fatalf("status=%d want=%d", w.Code, test.status)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWebStreamsRejectForeignOriginBeforeProvider(t *testing.T) {
	for _, desktop := range []bool{false, true} {
		s := &Server{PublicURL: "https://controller.example"}
		r := httptest.NewRequest("GET", s.PublicURL+"/v1/logical-boxes/box/terminal/stream", nil)
		r.Header.Set("Origin", "https://foreign.example")
		w := httptest.NewRecorder()
		s.workspaceStream(w, r, Principal{Role: "owner"}, desktop)
		if w.Code != 403 || s.webStreams != 0 {
			t.Fatal("cross-origin stream was allowed")
		}
	}
}
