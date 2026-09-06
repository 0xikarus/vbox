package controller

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const browserCookie = "vmbox_session"

type browserSession struct {
	token   string
	expires time.Time
}

// Sessions live only in controller memory. Every use rechecks the underlying
// access token, so revocation and role changes apply immediately. Restarting the
// controller requires browser login again, but does not stop worker sessions.
func (s *Server) browserOrigin(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.User != nil {
		return false
	}
	expected := strings.TrimRight(s.PublicURL, "/")
	if expected == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		expected = scheme + "://" + r.Host
	}
	return origin.String() == expected
}

func (s *Server) browserToken(r *http.Request) (string, bool) {
	// Browser GET requests need no Origin header, but reject cross-site fetches.
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return "", false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead || r.Header.Get("Upgrade") != "" {
		if !s.browserOrigin(r) {
			return "", false
		}
	}
	cookie, err := r.Cookie(browserCookie)
	if err != nil {
		return "", false
	}
	key := sha256.Sum256([]byte(cookie.Value))
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.browserSessions[key]
	if !ok {
		return "", false
	}
	if time.Now().After(session.expires) {
		delete(s.browserSessions, key)
		return "", false
	}
	return session.token, true
}

func (s *Server) browserLogin(w http.ResponseWriter, r *http.Request, _ Principal) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.browserOrigin(r) {
		writeError(w, 403, fmt.Errorf("same-origin browser login required"))
		return
	}
	token, ok := authorizationValue(r.Header.Get("Authorization"), "Bearer")
	if !ok {
		writeError(w, 401, fmt.Errorf("bearer token required for login"))
		return
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		writeError(w, 500, fmt.Errorf("cannot create browser session"))
		return
	}
	value := base64.RawURLEncoding.EncodeToString(entropy[:])
	now := time.Now()
	s.mu.Lock()
	if s.browserSessions == nil {
		s.browserSessions = make(map[[32]byte]browserSession)
	}
	for key, session := range s.browserSessions {
		if now.After(session.expires) {
			delete(s.browserSessions, key)
		}
	}
	if len(s.browserSessions) >= 1024 {
		s.mu.Unlock()
		writeError(w, 503, fmt.Errorf("browser session capacity reached"))
		return
	}
	if old, err := r.Cookie(browserCookie); err == nil {
		delete(s.browserSessions, sha256.Sum256([]byte(old.Value)))
	}
	s.browserSessions[sha256.Sum256([]byte(value))] = browserSession{token: token, expires: now.Add(8 * time.Hour)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: browserCookie, Value: value, Path: "/", HttpOnly: true, Secure: r.TLS != nil || strings.HasPrefix(s.PublicURL, "https://"), SameSite: http.SameSiteStrictMode, MaxAge: 8 * 60 * 60})
	w.WriteHeader(204)
}

func (s *Server) browserLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.browserOrigin(r) {
		writeError(w, 403, fmt.Errorf("same-origin browser logout required"))
		return
	}
	if cookie, err := r.Cookie(browserCookie); err == nil {
		s.mu.Lock()
		delete(s.browserSessions, sha256.Sum256([]byte(cookie.Value)))
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: browserCookie, Value: "", Path: "/", HttpOnly: true, Secure: r.TLS != nil || strings.HasPrefix(s.PublicURL, "https://"), SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(204)
}
