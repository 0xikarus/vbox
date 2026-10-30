package standalone

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPasswordGateAndChangeInvalidatesSessions(t *testing.T) {
	s, _ := setup(t)
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config/vmbox"), 0700)
	s.Runtime = &BoxRuntime{Home: home}
	s.Config.PasswordRequired = true
	auth := func(path, password, old string, cookie *http.Cookie, cap bool) *httptest.ResponseRecorder {
		t.Helper()
		s.lastAuth = time.Time{}
		b, _ := json.Marshal(map[string]string{"password": password, "oldPassword": old})
		r := httptest.NewRequest("POST", path, strings.NewReader(string(b)))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if cap {
			r.Header.Set("Authorization", "Bearer "+s.Config.Capabilities["desktop"])
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	root := request(s, "GET", "/", "", "", "").Body.String()
	if strings.Contains(root, s.Config.Capabilities["desktop"]) || !strings.Contains(root, "set workspace password") {
		t.Fatal("unprotected control page")
	}
	if w := auth("/auth/setup", "x", "", nil, false); w.Code != 403 {
		t.Fatal("unauthorized setup", w.Code)
	}
	w := auth("/auth/setup", "x", "", nil, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if w := request(s, "POST", "/v1/standalone/stream-ticket", "desktop", "", ""); w.Code != 403 {
		t.Fatal("capability bypassed password", w.Code)
	}
	r := httptest.NewRequest("POST", "/v1/standalone/stream-ticket", nil)
	r.AddCookie(cookie)
	r.Header.Set("Authorization", "Bearer "+s.Config.Capabilities["desktop"])
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("authorized controls denied", w.Code)
	}
	if w := auth("/auth/password", "y", "wrong-old", cookie, false); w.Code != 403 {
		t.Fatal("old password not required")
	}
	if w := auth("/auth/password", "y", "x", cookie, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if s.loggedIn(r) {
		t.Fatal("old browser session remains authorized")
	}
	if w := auth("/auth/login", "x", "", nil, false); w.Code != 401 {
		t.Fatal("old password still works")
	}
	if w := auth("/auth/login", "y", "", nil, false); w.Code != 200 {
		t.Fatal("new password does not work")
	}
}

func TestEmptyPasswordIsConfiguredAndCanLogIn(t *testing.T) {
	s, _ := setup(t)
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config/vmbox"), 0700)
	s.Runtime = &BoxRuntime{Home: home}
	s.Config.PasswordRequired = true
	w := request(s, "POST", "/auth/setup", "desktop", "", `{"password":""}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if s.passwordHash().Hash == "" || !s.passwordHash().check("") {
		t.Fatal("empty password not stored as a configured hash")
	}
	s.lastAuth = time.Time{}
	w = request(s, "POST", "/auth/login", "", "", `{"password":""}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
