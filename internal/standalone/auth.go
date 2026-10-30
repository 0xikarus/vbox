package standalone

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type PasswordHash struct {
	Salt string `json:"salt"`
	Hash string `json:"hash"`
}

func HashPassword(password string) (PasswordHash, error) {
	salt := randomToken()
	key, err := pbkdf2.Key(sha256.New, password, []byte(salt), 600000, 32)
	return PasswordHash{salt, hex.EncodeToString(key)}, err
}
func (h PasswordHash) check(password string) bool {
	key, err := pbkdf2.Key(sha256.New, password, []byte(h.Salt), 600000, 32)
	return err == nil && len(h.Hash) == 64 && subtle.ConstantTimeCompare([]byte(hex.EncodeToString(key)), []byte(h.Hash)) == 1
}

// VerifyPassword shares the standalone password format with the local hub.
func (h PasswordHash) VerifyPassword(password string) bool { return h.check(password) }
func passwordPath(home string) string                      { return filepath.Join(home, ".config/vmbox/web-password.json") }
func (s *Server) passwordHash() PasswordHash {
	var h PasswordHash
	if r, ok := s.Runtime.(*BoxRuntime); ok {
		b, _ := os.ReadFile(passwordPath(r.Home))
		_ = json.Unmarshal(b, &h)
	}
	return h
}
func (s *Server) loggedIn(r *http.Request) bool {
	cookie, err := r.Cookie("vbox_session")
	if err != nil {
		return false
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	expires, ok := s.sessions[cookie.Value]
	return ok && time.Now().Before(expires)
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	token := randomToken()
	s.authMu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]time.Time{}
	}
	for k, expires := range s.sessions {
		if time.Now().After(expires) {
			delete(s.sessions, k)
		}
	}
	s.sessions[token] = time.Now().Add(12 * time.Hour)
	s.authMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "vbox_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 43200})
}
func (s *Server) authPage(w http.ResponseWriter, r *http.Request) {
	setup := s.passwordHash().Hash == ""
	title := "unlock workspace"
	hint := "Enter the instance password to use desktop, tmux and chat."
	if setup {
		title = "set workspace password"
		hint = "Choose a password. Setup requires your existing private desktop link or an already-used browser."
	}
	body := `<h1>` + title + `</h1><p class="sub">` + hint + `</p><form id="auth"><div class="field"><label for="password">password</label><input id="password" type="password" autocomplete="current-password"></div><div class="form-actions"><button class="primary">unlock</button></div></form><p id="result" role="status"></p><script>
const setup=` + map[bool]string{true: "true", false: "false"}[setup] + `;
const token=location.hash.slice(1)||sessionStorage.getItem('vbox-standalone-token:'+window.VBOX_BASE_PATH+':desktop')||'';
if(token)sessionStorage.setItem('vbox-standalone-token:'+window.VBOX_BASE_PATH+':desktop',token);history.replaceState(null,'',location.pathname);
document.querySelector('#auth').onsubmit=async e=>{e.preventDefault();const r=await fetch(standalonePath('/auth/'+(setup?'setup':'login')),{method:'POST',headers:{'Content-Type':'application/json',Authorization:'Bearer '+token},body:JSON.stringify({password:document.querySelector('#password').value})});if(r.ok)location.reload();else document.querySelector('#result').textContent=await r.text()};</script>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, shell(title, body, s.Config.BasePath))
}
func (s *Server) auth(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "use POST", 405)
		return
	}
	var input struct {
		Password string `json:"password"`
		Old      string `json:"oldPassword"`
	}
	if !decode(w, r, &input) {
		return
	}

	s.authOp.Lock()
	defer s.authOp.Unlock()
	// Bound expensive password verification and make repeated guessing slow.
	if time.Since(s.lastAuth) < 500*time.Millisecond {
		http.Error(w, "try again shortly", 429)
		return
	}
	s.lastAuth = time.Now()
	current := s.passwordHash()
	switch r.URL.Path {
	case "/auth/login":
		if current.Hash == "" || !current.check(input.Password) {
			http.Error(w, "incorrect password", 401)
			return
		}
	case "/auth/setup":
		if current.Hash != "" {
			http.Error(w, "password already configured", 409)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.Config.Capabilities["desktop"])) != 1 {
			http.Error(w, "open your private desktop link to set the initial password", 403)
			return
		}
	case "/auth/password":
		if !s.loggedIn(r) || current.Hash == "" || !current.check(input.Old) {
			http.Error(w, "incorrect old password", 403)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	if r.URL.Path != "/auth/login" {
		runtime, ok := s.Runtime.(*BoxRuntime)
		if !ok {
			http.Error(w, "runtime unavailable", 503)
			return
		}
		h, err := HashPassword(input.Password)
		if err != nil {
			http.Error(w, "cannot hash password", 503)
			return
		}
		b, _ := json.Marshal(h)
		path := passwordPath(runtime.Home)
		if err = os.WriteFile(path+".tmp", b, 0600); err == nil {
			err = os.Rename(path+".tmp", path)
		}
		if err != nil {
			http.Error(w, "cannot save password", 503)
			return
		}
		s.authMu.Lock()
		clear(s.sessions)
		s.authMu.Unlock()
		s.mu.Lock()
		clear(s.tickets)
		for socket := range s.authSockets {
			socket.CloseNow()
		}
		s.mu.Unlock()
	}
	s.session(w, r)
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (s *Server) passwordPage(w http.ResponseWriter, r *http.Request) {
	if !s.loggedIn(r) {
		s.authPage(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, shell("change password", `<h1>change password</h1><p class="sub">The old password is required. Other sessions will be signed out.</p><form id="change"><div class="field"><label>old password<input type="password" id="old" autocomplete="current-password"></label><label>new password<input type="password" id="new" autocomplete="new-password"></label></div><div class="form-actions"><button class="primary">change password</button><a href="/">workspace</a></div></form><p id="result" role="status"></p><script>document.querySelector('#change').onsubmit=async e=>{e.preventDefault();const r=await fetch('/auth/password',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({oldPassword:document.querySelector('#old').value,password:document.querySelector('#new').value})});document.querySelector('#result').textContent=r.ok?'password changed':await r.text();if(r.ok)document.querySelector('#change').reset()}</script>`))
}
