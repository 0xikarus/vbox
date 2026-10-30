package standalone

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	webassets "github.com/0xikarus/vmbox-service/internal/controller/web"
	"github.com/0xikarus/vmbox-service/internal/desktop"
	"github.com/coder/websocket"
)

type Config struct {
	BasePath   string `json:"basePath,omitempty"`
	HubManaged bool   `json:"hubManaged,omitempty"`
	// The default unprotected dashboard exposes the browser entry points.
	// PrivateLinksOnly retains explicit capability-link discovery through CLI.
	PasswordRequired bool              `json:"passwordRequired,omitempty"`
	PrivateLinksOnly bool              `json:"privateLinksOnly,omitempty"`
	Assignment       string            `json:"assignment"`
	AgentToken       string            `json:"agentToken"`
	Capabilities     map[string]string `json:"capabilities"`
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func LoadConfig(dir string) (Config, error) {
	var c Config
	if err := os.MkdirAll(dir, 0700); err != nil {
		return c, err
	}
	path := filepath.Join(dir, "config.json")
	b, err := os.ReadFile(path)
	if err == nil {
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return c, fmt.Errorf("standalone identity must be a private regular file")
		}
		if err = json.Unmarshal(b, &c); err != nil {
			return c, err
		}
		if len(c.Assignment) != 64 || len(c.AgentToken) != 64 || len(c.Capabilities) != 3 {
			return c, fmt.Errorf("invalid standalone identity")
		}
		for _, scope := range []string{"view", "desktop", "chat"} {
			v := c.Capabilities[scope]
			if len(v) != 64 {
				return c, fmt.Errorf("invalid instance capability")
			}
			if _, err = hex.DecodeString(v); err != nil {
				return c, fmt.Errorf("invalid instance capability")
			}
		}
		return c, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	c = Config{Assignment: randomToken(), AgentToken: randomToken(), Capabilities: map[string]string{"view": randomToken(), "desktop": randomToken(), "chat": randomToken()}}
	b, err = json.Marshal(c)
	if err != nil {
		return c, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return c, err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return c, err
	}
	err = f.Close()
	return c, err
}

type Server struct {
	Store       *Store
	Config      Config
	Runtime     Runtime
	mu          sync.RWMutex
	startMu     sync.Mutex
	authMu      sync.Mutex
	authOp      sync.Mutex
	lastAuth    time.Time
	sessions    map[string]time.Time
	authSockets map[*websocket.Conn]bool
	Shutdown    func()
	state       string
	tickets     map[string]ticket
}
type ticket struct {
	Kind    string
	Scope   string
	Expires time.Time
}

func NewServer(store *Store, c Config, r Runtime) *Server {
	return &Server{Store: store, Config: c, Runtime: r, state: "starting", tickets: map[string]ticket{}}
}
func (s *Server) scope(r *http.Request) string {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	for scope, value := range s.Config.Capabilities {
		if s.Config.PasswordRequired && scope != "view" && !s.loggedIn(r) {
			continue
		}
		if len(value) == 64 && subtle.ConstantTimeCompare([]byte(token), []byte(value)) == 1 {
			return scope
		}
	}
	return ""
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https")
}
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid request", 400)
		return false
	}
	return true
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.dashboard)
	mux.HandleFunc("POST /auth/{action}", s.auth)
	mux.HandleFunc("GET /password", s.passwordPage)
	mux.HandleFunc("POST /v1/standalone/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if s.scope(r) != "desktop" {
			http.Error(w, "forbidden", 403)
			return
		}
		if s.Shutdown == nil {
			http.Error(w, "shutdown unavailable", 503)
			return
		}
		jsonResponse(w, 202, map[string]string{"state": "stopping"})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		go s.Shutdown()
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, map[string]bool{"ok": true}) })
	for asset, kind := range map[string]string{"inter-latin-wght-normal.woff2": "font/woff2", "vbox-logo.png": "image/png", "vbox-logo-dark.png": "image/png"} {
		mux.HandleFunc("GET /"+asset, func(w http.ResponseWriter, r *http.Request) {
			b, _ := webassets.ChatStyle.ReadFile(asset)
			w.Header().Set("Content-Type", kind)
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Write(b)
		})
	}
	mux.HandleFunc("GET /novnc.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := webassets.Desktop.ReadFile("novnc.js")
		w.Header().Set("Content-Type", "text/javascript")
		w.Write(b)
	})
	mux.HandleFunc("GET /workspace-desktop.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := webassets.Desktop.ReadFile("workspace-desktop.js")
		w.Header().Set("Content-Type", "text/javascript")
		w.Write(b)
	})
	for _, asset := range []string{"xterm.js", "xterm-fit.js", "workspace-terminal.js"} {
		mux.HandleFunc("GET /"+asset, func(w http.ResponseWriter, r *http.Request) {
			b, _ := webassets.Desktop.ReadFile(asset)
			w.Header().Set("Content-Type", "text/javascript")
			w.Write(b)
		})
	}
	for _, path := range []string{"/chat", "/view", "/desktop", "/terminal"} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			if s.Config.PasswordRequired && path != "/view" && !s.loggedIn(r) {
				s.authPage(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			io.WriteString(w, workspacePage(strings.TrimPrefix(r.URL.Path, "/"), s.Config.BasePath))
		})
	}
	mux.HandleFunc("GET /v1/standalone/status", func(w http.ResponseWriter, r *http.Request) {
		if s.scope(r) == "" {
			http.Error(w, "unauthorized", 401)
			return
		}
		s.mu.RLock()
		state := s.state
		s.mu.RUnlock()
		jsonResponse(w, 200, map[string]string{"state": state})
	})
	mux.HandleFunc("GET /v1/standalone/thinking", s.chat(s.thinking))
	mux.HandleFunc("POST /v1/standalone/thinking", s.chat(s.thinking))
	mux.HandleFunc("GET /v1/standalone/messages/{message}/images/{index}", s.chat(func(w http.ResponseWriter, r *http.Request) {
		index, err := strconv.Atoi(r.PathValue("index"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data, name, err := s.Store.Image(r.PathValue("message"), index)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		kind := http.DetectContentType(data)
		switch kind {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
		default:
			http.Error(w, "unsupported image", 415)
			return
		}
		w.Header().Set("Content-Type", kind)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
		w.Write(data)
	}))
	mux.HandleFunc("GET /v1/standalone/messages/html", s.chat(s.messageFragment))
	mux.HandleFunc("GET /v1/standalone/messages", s.chat(func(w http.ResponseWriter, r *http.Request) {
		m, e := s.Store.List()
		if e != nil {
			http.Error(w, "storage unavailable", 503)
			return
		}
		jsonResponse(w, 200, m)
	}))
	mux.HandleFunc("POST /v1/standalone/messages", s.chat(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Text string `json:"text"`
		}
		if !decode(w, r, &input) {
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if strings.TrimSpace(input.Text) == "" || len(key) < 1 || len(key) > 128 {
			http.Error(w, "text and Idempotency-Key required", 400)
			return
		}
		for _, c := range key {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				http.Error(w, "invalid Idempotency-Key", 400)
				return
			}
		}
		m, err := s.Store.Insert(key, "user", input.Text, "queued")
		if err != nil {
			status := 503
			if errors.Is(err, ErrConflict) {
				status = 409
			}
			http.Error(w, err.Error(), status)
			return
		}
		jsonResponse(w, 202, m)
	}))
	mux.HandleFunc("POST /v1/standalone/stream-ticket", func(w http.ResponseWriter, r *http.Request) {
		scope := s.scope(r)
		if scope != "desktop" {
			http.Error(w, "forbidden", 403)
			return
		}
		kind := r.URL.Query().Get("kind")
		if kind == "" {
			kind = "desktop"
		}
		if kind != "desktop" && kind != "terminal" {
			http.Error(w, "invalid stream kind", 400)
			return
		}
		id := randomToken()
		s.mu.Lock()
		for k, v := range s.tickets {
			if time.Now().After(v.Expires) {
				delete(s.tickets, k)
			}
		}
		if len(s.tickets) > 256 {
			s.mu.Unlock()
			http.Error(w, "too many tickets", 429)
			return
		}
		s.tickets[id] = ticket{Kind: kind, Scope: scope, Expires: time.Now().Add(30 * time.Second)}
		s.mu.Unlock()
		jsonResponse(w, 200, map[string]string{"ticket": id})
	})
	mux.HandleFunc("GET /v1/standalone/desktop/ws", s.stream)
	mux.HandleFunc("GET /v1/standalone/terminal/ws", s.stream)
	mux.HandleFunc("GET /v1/standalone/frame", func(w http.ResponseWriter, r *http.Request) {
		if s.scope(r) != "view" {
			http.Error(w, "forbidden", 403)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_ = s.Runtime.Frame(ctx, w)
	})
	mux.HandleFunc("/v1/agent-desktop/", s.agent)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !sameOrigin(r) {
			http.Error(w, "origin rejected", 403)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) chat(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.scope(r) != "chat" {
			http.Error(w, "forbidden", 403)
			return
		}
		next(w, r)
	}
}

var localTools = []string{"take_screenshot", "capture_window", "move_mouse", "click_mouse", "drag_mouse", "scroll_mouse", "type_text", "press_keys", "chat_message", "chat_ask", "heartbeat"}

func (s *Server) agent(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("DesktopAgent "+s.Config.AgentToken)) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "POST /v1/agent-desktop/start-session":
		if err := s.ensureStarted(r.Context()); err != nil {
			http.Error(w, "agent startup failed; retrying automatically", 503)
			return
		}
		jsonResponse(w, 200, map[string]bool{"ok": true})
	case "GET /v1/agent-desktop/tool-policy":
		jsonResponse(w, 200, map[string]any{"tools": localTools})
	case "POST /v1/agent-desktop/chat-ready": // The worker outbox is always the durable source.
		jsonResponse(w, 200, map[string]bool{"stored": false})
	case "POST /v1/agent-desktop/tool-activity", "POST /v1/agent-desktop/busy":
		jsonResponse(w, 200, map[string]bool{"ok": true})
	default:
		http.NotFound(w, r)
	}
}
func (s *Server) ensureStarted(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	call, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	err := s.Runtime.Start(call)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.state = "degraded"
	} else {
		s.state = "ready"
	}
	return err
}
func (s *Server) Run(ctx context.Context) {
	defer func() {
		if runtime, ok := s.Runtime.(interface{ Snapshot(context.Context) error }); ok {
			save, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = runtime.Snapshot(save)
		}
	}()
	// A transient native startup/restore failure must not strand durable chat.
	startupDone := make(chan struct{})
	go func() {
		defer close(startupDone)
		for {
			_ = s.ensureStarted(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			for {
				s.mu.RLock()
				ready := s.state == "ready"
				s.mu.RUnlock()
				if !ready {
					break
				}
				if runtime, ok := s.Runtime.(interface{ Healthy(context.Context) bool }); ok && !runtime.Healthy(ctx) {
					break
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
		}
	}()
	defer func() { <-startupDone }()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastSnapshot := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			ready := s.state == "ready"
			s.mu.RUnlock()
			if !ready {
				continue
			}
			s.tick(ctx)
			if time.Since(lastSnapshot) > 30*time.Second {
				if runtime, ok := s.Runtime.(interface{ Snapshot(context.Context) error }); ok {
					save, cancel := context.WithTimeout(ctx, 5*time.Second)
					_ = runtime.Snapshot(save)
					cancel()
				}
				lastSnapshot = time.Now()
			}
		}
	}
}
func (s *Server) tick(ctx context.Context) {
	for i := 0; i < 100; i++ {
		event, found, err := s.Runtime.Pull()
		if err != nil || !found {
			break
		}
		if event.Contact != "" {
			break
		}
		if err = s.Store.StoreEvent(event); err != nil {
			break
		}
		if err = s.Runtime.Ack(event.ID); err != nil {
			break
		}
	}
	pending, err := s.Store.Pending()
	if err != nil {
		return
	}
	for _, m := range pending {
		if ctx.Err() != nil {
			return
		}
		in := boxruntime.ChatInbound{ID: m.ID, Text: m.Text}
		call, cancel := context.WithTimeout(ctx, 20*time.Second)
		if m.State == "uncertain" {
			ok, _ := s.Runtime.Confirm(call, in)
			if ok {
				_ = s.Store.State(m.ID, "accepted")
			}
			cancel()
			continue
		}
		// Persist uncertainty before entering the native bridge. A crash never queues a fresh submission.
		if err = s.Store.State(m.ID, "uncertain"); err == nil {
			err = s.Runtime.Deliver(call, in)
			if err == nil {
				_ = s.Store.State(m.ID, "accepted")
			}
		}
		cancel()
	}
}

type socketWriter struct {
	ctx context.Context
	ws  *websocket.Conn
}

func (w socketWriter) Write(b []byte) (int, error) {
	ctx, c := context.WithTimeout(w.ctx, 15*time.Second)
	defer c()
	if err := w.ws.Write(ctx, websocket.MessageBinary, b); err != nil {
		return 0, err
	}
	return len(b), nil
}
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("ticket")
	s.mu.Lock()
	t, ok := s.tickets[id]
	delete(s.tickets, id)
	s.mu.Unlock()
	if !ok || time.Now().After(t.Expires) || (t.Kind == "terminal") != strings.Contains(r.URL.Path, "/terminal/") {
		http.Error(w, "invalid ticket", 401)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	if s.Config.PasswordRequired && !s.loggedIn(r) {
		return
	}
	s.mu.Lock()
	if s.authSockets == nil {
		s.authSockets = map[*websocket.Conn]bool{}
	}
	s.authSockets[ws] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.authSockets, ws); s.mu.Unlock() }()
	ws.SetReadLimit(128 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	in, out := io.Pipe()
	defer in.Close()
	defer out.Close()
	go func() {
		defer out.Close()
		defer cancel()
		for {
			kind, b, err := ws.Read(ctx)
			if err != nil || (t.Kind != "terminal" && kind != websocket.MessageBinary) || (t.Kind == "terminal" && kind != websocket.MessageText) {
				return
			}
			if t.Kind == "terminal" {
				b = append(b, '\n')
			}
			if _, err = out.Write(b); err != nil {
				return
			}
		}
	}()
	var source io.Reader = in
	if t.Scope == "view" {
		filtered, sink := io.Pipe()
		defer filtered.Close()
		source = filtered
		go func() { defer sink.Close(); _ = desktop.FilterViewOnlyRFB(sink, in) }()
	}
	if t.Kind == "terminal" {
		if runtime, ok := s.Runtime.(interface {
			Terminal(context.Context, io.Reader, io.Writer) error
		}); ok {
			_ = runtime.Terminal(ctx, source, socketWriter{ctx, ws})
		}
		return
	}
	_ = s.Runtime.Stream(ctx, source, socketWriter{ctx, ws})
}
