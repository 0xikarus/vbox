package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"github.com/creack/pty"
)

const profileLoginLifetime = 10 * time.Minute

type profileLoginManager struct {
	mu       sync.Mutex
	sessions map[string]*profileLoginSession
	commands map[string]string // fixed production commands; tests substitute synthetic CLIs
}

type profileLoginSession struct {
	mu             sync.Mutex
	id             string
	owner          Principal
	app            string
	name           string
	replace        bool
	expires        time.Time
	status         string
	url            string
	code           string
	message        string
	output         string // bounded, private CLI output; never serialized or logged
	terminalOutput []byte // short reconnect buffer, wiped when the helper exits
	watchers       map[chan []byte]struct{}
	pty            *os.File
	cancel         context.CancelFunc
}

type profileLoginView struct {
	ID        string           `json:"id"`
	Status    string           `json:"status"`
	URL       string           `json:"url,omitempty"`
	Code      string           `json:"code,omitempty"`
	Message   string           `json:"message,omitempty"`
	ExpiresAt time.Time        `json:"expiresAt"`
	Profile   *v1.LoginProfile `json:"profile,omitempty"`
}

var profileLoginURLs = regexp.MustCompile(`https://[^\s<>"']+`)
var profileDeviceCode = regexp.MustCompile(`\b[A-Z0-9]{4,8}-[A-Z0-9]{4,8}\b`)
var profileANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
var profileEmail = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func newProfileLoginManager() *profileLoginManager {
	return &profileLoginManager{sessions: make(map[string]*profileLoginSession), commands: map[string]string{"codex": "codex", "claude": "claude"}}
}

func (m *profileLoginManager) get(id string, p Principal) *profileLoginSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || s.owner.AccountID != p.AccountID || s.owner.UserID != p.UserID {
		return nil
	}
	return s
}

func (s *profileLoginSession) view() profileLoginView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return profileLoginView{ID: s.id, Status: s.status, URL: s.url, Code: s.code, Message: s.message, ExpiresAt: s.expires}
}

// Only official HTTPS login hosts may be sent to the browser. CLI output can
// contain arbitrary text, so links must never be forwarded without this check.
func officialProfileLoginURL(app, raw string) string {
	raw = strings.TrimRight(raw, ".,;)]}")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	allowed := app == "codex" && host == "auth.openai.com" && (u.Path == "/codex/device" || u.Path == "/codex/device/")
	if app == "claude" {
		allowed = host == "claude.ai" || host == "claude.com" || host == "console.anthropic.com" || host == "platform.claude.com"
	}
	if !allowed {
		return ""
	}
	for key := range u.Query() {
		switch strings.ToLower(key) {
		case "access_token", "refresh_token", "id_token", "api_key", "password":
			return ""
		}
	}
	return u.String()
}

func (s *profileLoginSession) ingest(output []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == "canceled" || s.status == "expired" {
		return
	}
	s.terminalOutput = append(s.terminalOutput, output...)
	if len(s.terminalOutput) > 65536 {
		s.terminalOutput = append([]byte(nil), s.terminalOutput[len(s.terminalOutput)-65536:]...)
	}
	for watcher := range s.watchers {
		select {
		case watcher <- append([]byte(nil), output...):
		default:
			close(watcher)
			delete(s.watchers, watcher)
		}
	}
	s.output += profileANSI.ReplaceAllString(string(output), "")
	if len(s.output) > 16384 {
		s.output = s.output[len(s.output)-16384:]
	}
	for _, raw := range profileLoginURLs.FindAllString(s.output, -1) {
		if validated := officialProfileLoginURL(s.app, raw); validated != "" {
			s.url = validated
		}
	}
	if s.app == "codex" && s.url != "" {
		if code := profileDeviceCode.FindString(s.output); code != "" {
			s.code = code
			s.status = "waiting"
		}
	}
	if s.app == "claude" && s.url != "" {
		s.status = "waiting"
	}
}

func (s *profileLoginSession) finish(status, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != "canceled" && s.status != "expired" {
		s.status, s.message = status, message
	}
	s.output = ""
	s.code = ""
	clear(s.terminalOutput)
	s.terminalOutput = nil
	for watcher := range s.watchers {
		close(watcher)
		delete(s.watchers, watcher)
	}
	s.pty = nil
}

func (s *Server) startBrowserProfileLogin(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		Application     string `json:"application"`
		Name            string `json:"name"`
		Email           string `json:"email"`
		ReplaceExisting bool   `json:"replaceExisting"`
	}
	if decodeJSON(r, &req) != nil || (req.Application != "codex" && req.Application != "claude") || !loginProfileName.MatchString(req.Name) || len(req.Email) > 254 || (req.Email != "" && !profileEmail.MatchString(req.Email)) {
		writeError(w, 400, fmt.Errorf("choose Codex or Claude and a valid profile name"))
		return
	}
	var exists bool
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM login_profiles WHERE account_id=$1 AND application=$2 AND name=$3)`, p.AccountID, req.Application, req.Name).Scan(&exists); err != nil {
		writeError(w, 500, fmt.Errorf("could not check profile name"))
		return
	}
	if exists != req.ReplaceExisting {
		if exists {
			writeError(w, 409, fmt.Errorf("profile name already exists; select Replace this saved profile to update it"))
		} else {
			writeError(w, 409, fmt.Errorf("no saved profile with that name exists to replace"))
		}
		return
	}
	manager := s.profileLogins
	manager.mu.Lock()
	active := 0
	for _, current := range manager.sessions {
		if current.owner.AccountID == p.AccountID && current.owner.UserID == p.UserID {
			current.mu.Lock()
			if current.status == "starting" || current.status == "waiting" {
				active++
			}
			current.mu.Unlock()
		}
	}
	if active >= 3 {
		manager.mu.Unlock()
		writeError(w, 429, fmt.Errorf("finish or cancel an active login first"))
		return
	}
	path, err := exec.LookPath(manager.commands[req.Application])
	if err != nil {
		manager.mu.Unlock()
		writeError(w, 503, fmt.Errorf("%s login helper is unavailable on this controller", req.Application))
		return
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		manager.mu.Unlock()
		writeError(w, 500, fmt.Errorf("could not start login"))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), profileLoginLifetime)
	session := &profileLoginSession{id: hex.EncodeToString(random[:]), owner: p, app: req.Application, name: req.Name, replace: req.ReplaceExisting, expires: time.Now().Add(profileLoginLifetime), status: "starting", cancel: cancel}
	manager.sessions[session.id] = session
	manager.mu.Unlock()
	go s.runBrowserProfileLogin(ctx, session, path, req.Email)
	time.AfterFunc(profileLoginLifetime+time.Minute, func() { manager.mu.Lock(); delete(manager.sessions, session.id); manager.mu.Unlock() })
	writeJSON(w, http.StatusAccepted, session.view())
}

func isolatedProfileEnv(home, app string) []string {
	env := []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "TMPDIR=" + filepath.Join(home, "tmp"), "PATH=/usr/local/bin:/usr/bin:/bin", "TERM=xterm", "LANG=C.UTF-8", "CI=1"}
	if app == "codex" {
		env = append(env, "CODEX_HOME="+filepath.Join(home, ".codex"))
	}
	if app == "claude" {
		env = append(env, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"))
	}
	return env
}

func readProfileLoginFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512*1024 {
		return nil, fmt.Errorf("login credential file is invalid or too large")
	}
	data, err := io.ReadAll(io.LimitReader(file, 512*1024+1))
	if err != nil || len(data) > 512*1024 {
		clear(data)
		return nil, fmt.Errorf("login credential file is invalid or too large")
	}
	return data, nil
}

func (s *Server) runBrowserProfileLogin(ctx context.Context, session *profileLoginSession, executable, email string) {
	defer session.cancel()
	home, err := os.MkdirTemp("", "vmbox-profile-login-")
	if err != nil {
		session.finish("failed", "Could not create isolated login workspace.")
		return
	}
	defer os.RemoveAll(home)
	if err := os.Chmod(home, 0o700); err != nil {
		session.finish("failed", "Could not secure login workspace.")
		return
	}
	if err := os.Mkdir(filepath.Join(home, "tmp"), 0o700); err != nil {
		session.finish("failed", "Could not create login workspace.")
		return
	}
	args := []string{"login", "--device-auth"}
	if session.app == "codex" {
		config := filepath.Join(home, ".codex")
		if os.Mkdir(config, 0o700) != nil || os.WriteFile(filepath.Join(config, "config.toml"), []byte("cli_auth_credentials_store = \"file\"\n"), 0o600) != nil {
			session.finish("failed", "Could not configure Codex login.")
			return
		}
		args = []string{"login", "--device-auth"}
	} else {
		args = []string{"auth", "login", "--claudeai"}
		if email != "" {
			args = append(args, "--email", email)
		}
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = home
	cmd.Env = isolatedProfileEnv(home, session.app)
	terminal, err := pty.Start(cmd)
	if err != nil {
		session.finish("failed", "Login helper could not start. Check its installed version and retry.")
		return
	}
	session.mu.Lock()
	session.pty = terminal
	session.mu.Unlock()
	// pty.Start makes a new session/process group. A timeout or cancellation
	// must stop child browser helpers as well as the CLI itself.
	processDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-processDone:
		}
	}()
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, err := terminal.Read(buf)
			if n > 0 {
				session.ingest(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	waitErr := cmd.Wait()
	close(processDone)
	_ = terminal.Close()
	<-readDone
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			session.finish("expired", "Login timed out. Start again.")
		} else {
			session.finish("canceled", "Login canceled.")
		}
		return
	}
	if waitErr != nil {
		session.finish("failed", "The login helper stopped before sign-in completed. Retry; if this repeats, update the controller login helper.")
		return
	}
	files := map[string][]byte{}
	if session.app == "codex" {
		files["auth.json"], err = readProfileLoginFile(filepath.Join(home, ".codex", "auth.json"))
		if err == nil {
			files["config.toml"], err = readProfileLoginFile(filepath.Join(home, ".codex", "config.toml"))
		}
	} else {
		files[".credentials.json"], err = readProfileLoginFile(filepath.Join(home, ".claude", ".credentials.json"))
		if data, e := readProfileLoginFile(filepath.Join(home, ".claude", "settings.json")); e == nil {
			files["settings.json"] = data
		} else if !errors.Is(e, os.ErrNotExist) {
			err = e
		}
		if data, e := readProfileLoginFile(filepath.Join(home, ".claude.json")); e == nil {
			files[".claude.json"] = data
		} else if !errors.Is(e, os.ErrNotExist) {
			err = e
		}
	}
	defer func() {
		for _, data := range files {
			clear(data)
		}
	}()
	if err != nil || loginprofile.Validate(session.app, files, time.Now()) != nil || len(files["auth.json"])+len(files[".credentials.json"]) > 512*1024 {
		session.finish("failed", "Login completed without usable credentials. Retry with a current CLI version.")
		return
	}
	if ctx.Err() != nil {
		session.finish("canceled", "Login canceled.")
		return
	}
	req := v1.SaveLoginProfileRequest{Files: files, ReplaceExisting: session.replace}
	_, err = s.Store.SaveLoginProfile(ctx, session.owner, session.app, session.name, req)
	if err != nil {
		session.finish("failed", "Login succeeded but the profile could not be saved. Check the name and retry.")
		return
	}
	session.finish("saved", "Profile saved. Select it when creating a box, or apply it to an existing box in Credentials.")
}

func (s *Server) browserProfileLoginStatus(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	session := s.profileLogins.get(r.PathValue("id"), p)
	if session == nil {
		writeError(w, 404, fmt.Errorf("login session not found"))
		return
	}
	writeJSON(w, 200, session.view())
}

func (s *Server) browserProfileLoginCode(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	session := s.profileLogins.get(r.PathValue("id"), p)
	if session == nil {
		writeError(w, 404, fmt.Errorf("login session not found"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		Code string `json:"code"`
	}
	if decodeJSON(r, &req) != nil || len(req.Code) == 0 || len(req.Code) > 2048 || strings.ContainsAny(req.Code, "\r\n\x00") {
		writeError(w, 400, fmt.Errorf("enter the browser verification code"))
		return
	}
	session.mu.Lock()
	if session.app != "claude" || session.status != "waiting" || session.pty == nil {
		session.mu.Unlock()
		writeError(w, 409, fmt.Errorf("Claude is not waiting for a browser code"))
		return
	}
	terminal := session.pty
	session.mu.Unlock()
	if _, err := io.WriteString(terminal, req.Code+"\r"); err != nil {
		writeError(w, 409, fmt.Errorf("could not submit browser code; retry login"))
		return
	}
	writeJSON(w, 200, session.view())
}

func (s *Server) cancelBrowserProfileLogin(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	session := s.profileLogins.get(r.PathValue("id"), p)
	if session == nil {
		writeError(w, 404, fmt.Errorf("login session not found"))
		return
	}
	session.mu.Lock()
	if session.status == "starting" || session.status == "waiting" {
		session.status = "canceled"
		session.message = "Login canceled."
		session.cancel()
	}
	session.mu.Unlock()
	writeJSON(w, 200, session.view())
}

// Called when the controller stops, so no CLI survives its owner process.
func (s *Server) CloseProfileLogins() {
	s.profileLogins.mu.Lock()
	defer s.profileLogins.mu.Unlock()
	for _, session := range s.profileLogins.sessions {
		session.cancel()
	}
}
