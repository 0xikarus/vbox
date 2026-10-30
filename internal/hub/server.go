package hub

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	webassets "github.com/0xikarus/vmbox-service/internal/controller/web"
	"github.com/0xikarus/vmbox-service/internal/modelcatalog"
	"github.com/0xikarus/vmbox-service/internal/standalone"
)

type session struct {
	Expires time.Time
	Context context.Context
	Cancel  context.CancelFunc
}
type Server struct {
	context      context.Context
	Manager      *Manager
	mu           sync.Mutex
	sessions     map[string]session
	password     standalone.PasswordHash
	token        string
	lastLogin    time.Time
	clientMu     sync.Mutex
	clients      map[string]*workerClient
	CatalogQuery func(context.Context, string, map[string][]byte) ([]modelcatalog.Choice, error)
	catalogMu    sync.Mutex
	catalogCache map[string]cachedCatalog
	catalogSlot  chan struct{}
}

func NewServer(m *Manager) *Server {
	c := m.config()
	s := &Server{context: context.Background(), Manager: m, password: c.Password, token: c.APIToken, sessions: map[string]session{}, clients: map[string]*workerClient{}}
	s.catalogCache = map[string]cachedCatalog{}
	s.catalogSlot = make(chan struct{}, 1)
	s.CatalogQuery = s.queryLiveModels
	m.ready = s.ready
	m.goal = s.goal
	return s
}
func sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, e := url.Parse(o)
		return e == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https")
	}
	return true
}
func output(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func body(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 6<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		http.Error(w, "invalid request", 400)
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		http.Error(w, "invalid request", 400)
		return false
	}
	return true
}
func (s *Server) authorized(r *http.Request) (context.Context, bool) {
	if !sameOrigin(r) {
		return nil, false
	}
	if len(s.token) == 64 && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.token)) == 1 {
		return s.context, true
	}
	c, err := r.Cookie("vbox_hub_session")
	if err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.sessions[c.Value]
	if !ok || time.Now().After(x.Expires) {
		return nil, false
	}
	return x.Context, true
}
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.authorized(r); !ok {
			http.Error(w, "hub authentication required", 401)
			return
		}
		next(w, r)
	}
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origin rejected", 403)
		return
	}
	var in struct {
		Password string `json:"password"`
		Old      string `json:"oldPassword"`
	}
	if !body(w, r, &in) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.lastLogin) < 500*time.Millisecond {
		http.Error(w, "try again shortly", 429)
		return
	}
	s.lastLogin = time.Now()
	change := r.URL.Path == "/auth/password"
	if change {
		c, err := r.Cookie("vbox_hub_session")
		x, ok := s.sessions[func() string {
			if err == nil {
				return c.Value
			}
			return ""
		}()]
		if !ok || time.Now().After(x.Expires) || !s.password.VerifyPassword(in.Old) {
			http.Error(w, "incorrect old password", 403)
			return
		}
	} else if !s.password.VerifyPassword(in.Password) {
		http.Error(w, "incorrect password", 401)
		return
	}
	if change {
		h, err := standalone.HashPassword(in.Password)
		if err != nil {
			http.Error(w, "password update failed", 503)
			return
		}
		s.Manager.mu.Lock()
		c := s.Manager.Config
		c.Password = h
		err = SaveConfig(s.Manager.Dir, c)
		if err == nil {
			s.Manager.Config = c
		}
		s.Manager.mu.Unlock()
		if err != nil {
			http.Error(w, "password update failed", 503)
			return
		}
		s.password = h
		for k, x := range s.sessions {
			x.Cancel()
			delete(s.sessions, k)
		}
	}
	token := Token()
	ctx, cancel := context.WithDeadline(s.context, time.Now().Add(12*time.Hour))
	s.sessions[token] = session{time.Now().Add(12 * time.Hour), ctx, cancel}
	http.SetCookie(w, &http.Cookie{Name: "vbox_hub_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 43200})
	output(w, 200, map[string]bool{"ok": true})
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for asset, kind := range map[string]string{"vbox-logo.png": "image/png", "vbox-logo-dark.png": "image/png", "inter-latin-wght-normal.woff2": "font/woff2"} {
		mux.HandleFunc("GET /"+asset, func(w http.ResponseWriter, r *http.Request) {
			b, _ := webassets.ChatStyle.ReadFile(asset)
			w.Header().Set("Content-Type", kind)
			w.Write(b)
		})
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { output(w, 200, map[string]bool{"ok": true}) })
	mux.HandleFunc("POST /auth/login", s.login)
	mux.HandleFunc("POST /auth/password", s.login)
	mux.HandleFunc("POST /auth/logout", s.guard(func(w http.ResponseWriter, r *http.Request) {
		if c, e := r.Cookie("vbox_hub_session"); e == nil {
			s.mu.Lock()
			if x, ok := s.sessions[c.Value]; ok {
				x.Cancel()
				delete(s.sessions, c.Value)
			}
			s.mu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: "vbox_hub_session", Path: "/", MaxAge: -1})
		output(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /password", s.passwordPage)
	mux.HandleFunc("GET /api/status", s.guard(func(w http.ResponseWriter, r *http.Request) {
		c := s.Manager.config()
		h, err := s.Manager.Engine.Host(r.Context())
		if err != nil {
			http.Error(w, "host telemetry unavailable", 503)
			return
		}
		boxes, err := s.Manager.Store.List()
		if err != nil {
			http.Error(w, "storage unavailable", 503)
			return
		}
		cpu, mem, n := allocated(boxes)
		output(w, 200, map[string]any{"policy": c.Policy, "host": h, "allocated": map[string]any{"cpus": cpu, "memoryMiB": mem, "running": n}, "defaults": c.Defaults})
	}))
	mux.HandleFunc("PUT /api/policy", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var p Policy
		if !body(w, r, &p) {
			return
		}
		if err := s.Manager.SetPolicy(r.Context(), p); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		output(w, 200, p)
	}))
	mux.HandleFunc("GET /api/boxes", s.guard(func(w http.ResponseWriter, r *http.Request) {
		b, err := s.Manager.Store.List()
		if err != nil {
			http.Error(w, "storage unavailable", 503)
			return
		}
		for i := range b {
			b[i] = s.Manager.publicBox(b[i])
		}
		output(w, 200, b)
	}))
	mux.HandleFunc("POST /api/boxes", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var spec Spec
		if !body(w, r, &spec) {
			return
		}
		b, err := s.Manager.Create(spec, r.Header.Get("Idempotency-Key"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		output(w, 202, b)
	}))
	mux.HandleFunc("GET /api/boxes/{id}", s.guard(func(w http.ResponseWriter, r *http.Request) {
		b, err := s.Manager.Store.Get(r.PathValue("id"))
		if err != nil || b.State == "deleted" {
			http.NotFound(w, r)
			return
		}
		output(w, 200, s.Manager.publicBox(b))
	}))
	mux.HandleFunc("POST /api/boxes/{id}/{action}", s.guard(func(w http.ResponseWriter, r *http.Request) {
		action := r.PathValue("action")
		if action == "resources" {
			var in struct {
				CPU       float64 `json:"cpu"`
				MemoryMiB int64   `json:"memoryMiB"`
			}
			if !body(w, r, &in) {
				return
			}
			if err := s.Manager.Resize(r.Context(), r.PathValue("id"), in.CPU, in.MemoryMiB); err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			output(w, 200, map[string]bool{"ok": true})
			return
		}

		if action == "messages" {
			s.messages(w, r)
			return
		}
		if err := s.Manager.Action(r.PathValue("id"), action); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		output(w, 202, map[string]string{"action": action})
	}))
	mux.HandleFunc("GET /api/boxes/{id}/messages", s.guard(s.messages))
	mux.HandleFunc("GET /api/boxes/{id}/messages/{message}/images/{index}", s.guard(s.image))
	mux.HandleFunc("GET /api/profiles", s.guard(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.Manager.Store.Profiles()
		if err != nil {
			http.Error(w, "storage unavailable", 503)
			return
		}
		output(w, 200, p)
	}))
	mux.HandleFunc("GET /api/profiles/{id}/models", s.guard(s.profileModels))
	mux.HandleFunc("GET /api/models", s.guard(s.localModels))
	mux.HandleFunc("DELETE /api/profiles/{id}", s.guard(func(w http.ResponseWriter, r *http.Request) {
		s.Manager.mu.Lock()
		defer s.Manager.mu.Unlock()
		if err := s.Manager.Store.DeleteProfile(r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		output(w, 200, map[string]bool{"deleted": true})
	}))
	mux.HandleFunc("POST /api/profiles", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name    string            `json:"name"`
			Agent   string            `json:"agent"`
			Archive []byte            `json:"archive"`
			Files   map[string][]byte `json:"files"`
		}
		if !body(w, r, &in) {
			return
		}
		defer clear(in.Archive)
		for _, b := range in.Files {
			defer clear(b)
		}
		if len(in.Files) > 0 {
			if len(in.Archive) > 0 {
				http.Error(w, "choose files or an archive", 400)
				return
			}
			var err error
			in.Archive, err = ProfileFilesArchive(in.Agent, in.Files)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			defer clear(in.Archive)
		}

		p, err := s.Manager.Store.SaveProfile(in.Name, in.Agent, in.Archive)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		output(w, 201, p)
	}))
	mux.HandleFunc("/boxes/{id}/{path...}", s.proxy)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
}
func Serve(ctx context.Context, dir, listen string) error {
	c, err := LoadConfig(dir)
	if err != nil {
		return fmt.Errorf("initialize the hub before serving: %w", err)
	}
	if listen != "" {
		c.Listen = listen
	}
	store, err := OpenStore(dir)
	if err != nil {
		return err
	}
	defer store.Close()
	engine := NewDockerEngine(c.ID, c.Image, dir)
	h, err := engine.Host(ctx)
	if err != nil {
		return err
	}
	if err = c.Policy.Validate(h); err != nil {
		return err
	}
	m := NewManager(dir, c, store, engine)
	s := NewServer(m)
	ctx, cancel := context.WithCancel(ctx)
	s.context = ctx
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	defer func() { cancel(); <-done }()
	srv := &http.Server{Addr: c.Listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 65536}
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = srv.Shutdown(shutdown)
		_ = srv.Close()
	}()
	err = srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
