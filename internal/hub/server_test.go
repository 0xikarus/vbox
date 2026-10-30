package hub

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/standalone"
	"github.com/coder/websocket"
)

func authenticated(t *testing.T) (*Server, string) {
	t.Helper()
	m, _ := fixture(t)
	h, err := standalone.HashPassword("x")
	if err != nil {
		t.Fatal(err)
	}
	m.Config.Password = h
	m.Config.APIToken = Token()
	s := NewServer(m)
	r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"password":"x"}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	return s, w.Result().Cookies()[0].String()
}
func call(s *Server, method, path, cookie, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Cookie", cookie)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestAPIAuthenticationAndSecretRedaction(t *testing.T) {
	s, cookie := authenticated(t)
	if w := call(s, "GET", "/api/boxes", "", ""); w.Code != 401 {
		t.Fatal("API exposed without login")
	}
	r := httptest.NewRequest("POST", "/api/boxes", strings.NewReader(`{"name":"evil"}`))
	r.Header.Set("Cookie", cookie)
	r.Header.Set("Origin", "https://other-host.invalid")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("cross-origin mutation accepted")
	}
	w = call(s, "POST", "/api/boxes", cookie, `{"name":"safe"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var b Box
	if json.Unmarshal(w.Body.Bytes(), &b) != nil {
		t.Fatal("invalid box")
	}
	private, _ := s.Manager.Store.Get(b.ID)
	for _, path := range []string{"/api/boxes", "/api/boxes/" + b.ID, "/api/status"} {
		w = call(s, "GET", path, cookie, "")
		if strings.Contains(w.Body.String(), private.Password) || strings.Contains(w.Body.String(), s.token) {
			t.Fatal("secret in public response")
		}
	}
	r = httptest.NewRequest("GET", "/api/boxes", nil)
	r.Header.Set("Authorization", "Bearer "+s.token)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("API token not accepted")
	}
	if w = call(s, "POST", "/auth/logout", cookie, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = call(s, "GET", "/api/boxes", cookie, ""); w.Code != 401 {
		t.Fatal("logout left session valid")
	}
}
func TestProxyScopesAndLogoutDisconnectsViewer(t *testing.T) {
	s, cookie := authenticated(t)
	closed := make(chan struct{}, 1)
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "vbox_session", Value: "private-upstream", Path: "/"})
			io.WriteString(w, `{"ok":true}`)
		case "/v1/standalone/desktop/ws":
			if _, err := r.Cookie("vbox_session"); err != nil {
				http.Error(w, "no session", 403)
				return
			}
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			defer c.CloseNow()
			_, _, _ = c.Read(r.Context())
			closed <- struct{}{}
		default:
			if r.Header.Get("Cookie") != "" {
				io.WriteString(w, "owner")
			} else {
				http.Error(w, "read-only token required", 403)
			}
		}
	}))
	defer worker.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(worker.URL, "http://127.0.0.1:"))
	f := s.Manager.Engine.(*fakeEngine)
	f.gateway = Gateway{Port: port, Capabilities: map[string]string{"chat": strings.Repeat("c", 64)}}
	b, _ := s.Manager.Create(Spec{Name: "viewer"})
	b.State = "running"
	b.Port = port
	if err := s.Manager.Store.Update(b); err != nil {
		t.Fatal(err)
	}
	w := call(s, "GET", b.URL, "", "")
	if w.Code != 303 {
		t.Fatal("private proxy accessible without hub login")
	}
	w = call(s, "GET", b.URL+"v1/standalone/frame", "", "")
	if w.Code != 403 {
		t.Fatal("public viewer received private worker cookie")
	}
	w = call(s, "GET", b.URL, cookie, "")
	if w.Code != 200 || w.Body.String() != "owner" {
		t.Fatalf("owner proxy failed: %d", w.Code)
	}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{}
	header.Set("Cookie", cookie)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+b.URL+"v1/standalone/desktop/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if w = call(s, "POST", "/auth/logout", cookie, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("logged-out viewer retained control")
	}
}
func TestProfileArchiveRejectsLinksAndTraversal(t *testing.T) {
	s, _ := fixture(t)
	for _, h := range []*tar.Header{{Name: "../../escape", Size: 1, Mode: 0600}, {Name: ".codex/auth.json", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}} {
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			tw.Write([]byte("x"))
		}
		tw.Close()
		if _, err := s.Store.SaveProfile("unsafe", "codex", b.Bytes()); err == nil {
			t.Fatal("unsafe archive accepted")
		}
	}
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	tw.WriteHeader(&tar.Header{Name: ".codex/auth.json", Size: 2, Mode: 0777, Uid: 0})
	tw.Write([]byte("{}"))
	tw.Close()
	p, err := s.Store.SaveProfile("portable", "codex", b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Store.Archive([]string{p.ID})
	if err != nil {
		t.Fatal(err)
	}
	h, err := tar.NewReader(bytes.NewReader(a[0])).Next()
	if err != nil || h.Uid != 10001 || h.Mode != 0600 {
		t.Fatal("profile ownership not sanitized")
	}
}
