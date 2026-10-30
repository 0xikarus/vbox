package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/standalone"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type workerClient struct {
	mu      sync.Mutex
	gateway Gateway
	cookie  string
	until   time.Time
}

var workerHTTP = &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (s *Server) worker(ctx context.Context, b Box) (*workerClient, error) {
	s.clientMu.Lock()
	c := s.clients[b.ID]
	if c == nil {
		c = &workerClient{}
		s.clients[b.ID] = c
	}
	s.clientMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cookie != "" && time.Now().Before(c.until) && c.gateway.Port == b.Port {
		return c, nil
	}
	g, err := s.Manager.Engine.Gateway(ctx, b)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(map[string]string{"password": b.Password})
	req, err := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:"+strconv.Itoa(g.Port)+"/auth/login", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := workerHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("worker authentication unavailable")
	}
	cookie := ""
	for _, v := range resp.Cookies() {
		if v.Name == "vbox_session" {
			cookie = v.Name + "=" + v.Value
		}
	}
	if cookie == "" {
		return nil, fmt.Errorf("worker session unavailable")
	}
	c.gateway = g
	c.cookie = cookie
	c.until = time.Now().Add(10 * time.Hour)
	return c, nil
}
func (s *Server) workerRequest(ctx context.Context, b Box, method, path string, data io.Reader, key string) (*http.Response, error) {
	c, err := s.worker(ctx, b)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	g, cookie := c.gateway, c.cookie
	c.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:"+strconv.Itoa(g.Port)+path, data)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Authorization", "Bearer "+g.Capabilities["chat"])
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := workerHTTP.Do(req)
	if err == nil && resp.StatusCode == 403 {
		c.mu.Lock()
		c.until = time.Time{}
		c.mu.Unlock()
	}
	return resp, err
}
func (s *Server) ready(ctx context.Context, b Box) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	r, err := s.workerRequest(ctx, b, "GET", "/v1/standalone/status", nil, "")
	if err != nil {
		return false
	}
	defer r.Body.Close()
	var state struct{ State string }
	return r.StatusCode == 200 && json.NewDecoder(r.Body).Decode(&state) == nil && state.State == "ready"
}
func (s *Server) goal(ctx context.Context, b Box) error {
	data, _ := json.Marshal(map[string]string{"text": b.Spec.Goal})
	r, err := s.workerRequest(ctx, b, "POST", "/v1/standalone/messages", bytes.NewReader(data), "hub-goal-"+b.ID)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != 202 {
		return fmt.Errorf("goal delivery not accepted")
	}
	return nil
}
func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	b, err := s.Manager.Store.Get(r.PathValue("id"))
	if err != nil || b.State != "running" || b.Desired != "running" {
		http.Error(w, "box is not ready", 409)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if r.Method == "POST" && (key == "" || len(key) > 128) {
		http.Error(w, "Idempotency-Key required", 400)
		return
	}
	resp, err := s.workerRequest(r.Context(), b, r.Method, "/v1/standalone/messages", http.MaxBytesReader(w, r.Body, 1<<20), key)
	if err != nil {
		http.Error(w, "worker unavailable", 503)
		return
	}
	defer resp.Body.Close()
	if r.Method == "GET" && resp.StatusCode == 200 {
		var messages []standalone.Message
		if json.NewDecoder(io.LimitReader(resp.Body, 6<<20)).Decode(&messages) != nil {
			http.Error(w, "invalid message history", 503)
			return
		}
		for i := range messages {
			for j := range messages[i].Images {
				messages[i].Images[j].URL = "/api/boxes/" + b.ID + "/messages/" + url.PathEscape(messages[i].ID) + "/images/" + strconv.Itoa(messages[i].Images[j].Index)
			}
		}
		output(w, 200, messages)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 4<<20))
}
func (s *Server) proxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID(id) {
		http.NotFound(w, r)
		return
	}
	b, err := s.Manager.Store.Get(id)
	if err != nil || b.State == "deleted" {
		http.NotFound(w, r)
		return
	}
	suffix := "/" + r.PathValue("path")
	_, owner := s.authorized(r)
	publicAsset := map[string]bool{"/novnc.js": true, "/workspace-desktop.js": true, "/workspace-terminal.js": true, "/xterm.js": true, "/xterm-fit.js": true, "/inter-latin-wght-normal.woff2": true, "/vbox-logo.png": true, "/vbox-logo-dark.png": true}
	public := r.Method == "GET" && (suffix == "/view" || suffix == "/v1/standalone/frame" || suffix == "/v1/standalone/status" || publicAsset[suffix])
	if !owner && !public {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if suffix == "/password" || strings.HasPrefix(suffix, "/auth/") {
		http.Error(w, "manage the hub password at /password", 403)
		return
	}
	if suffix == "/v1/standalone/shutdown" && r.Method == "POST" {
		if !owner {
			http.Error(w, "authentication required", 401)
			return
		}
		if err = s.Manager.Action(id, "stop"); err != nil {
			http.Error(w, "stop failed", 409)
			return
		}
		output(w, 202, map[string]bool{"ok": true})
		return
	}
	if b.State != "running" && b.State != "starting" {
		http.Error(w, "box is not running", 409)
		return
	}
	if b.Port < 1 {
		http.Error(w, "worker starting", 503)
		return
	}
	cookie := ""
	if owner {
		c, e := s.worker(r.Context(), b)
		if e != nil {
			http.Error(w, "worker initializing", 503)
			return
		}
		c.mu.Lock()
		cookie = c.cookie
		c.mu.Unlock()
	}
	target, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(b.Port))
	proxy := &httputil.ReverseProxy{Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(target)
		p.Out.URL.Path = suffix
		p.Out.URL.RawPath = ""
		p.Out.Header.Del("Cookie")
		p.Out.Header.Del("Origin")
		if cookie != "" {
			p.Out.Header.Set("Cookie", cookie)
		}
	}, ModifyResponse: func(resp *http.Response) error {
		resp.Header.Del("Set-Cookie")
		resp.Header.Set("Cache-Control", "no-store")
		if resp.StatusCode == 403 && owner {
			s.clientMu.Lock()
			c := s.clients[b.ID]
			s.clientMu.Unlock()
			if c != nil {
				c.mu.Lock()
				c.until = time.Time{}
				c.mu.Unlock()
			}
		}
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, e error) {
		http.Error(w, "worker connection unavailable", 503)
	}}
	if owner {
		sessionCtx, _ := s.authorized(r)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		stop := context.AfterFunc(sessionCtx, cancel)
		defer stop()
		r = r.WithContext(ctx)
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) image(w http.ResponseWriter, r *http.Request) {
	b, err := s.Manager.Store.Get(r.PathValue("id"))
	if err != nil || b.State != "running" {
		http.Error(w, "box not ready", 409)
		return
	}
	path := "/v1/standalone/messages/" + url.PathEscape(r.PathValue("message")) + "/images/" + url.PathEscape(r.PathValue("index"))
	resp, err := s.workerRequest(r.Context(), b, "GET", path, nil, "")
	if err != nil {
		http.Error(w, "worker unavailable", 503)
		return
	}
	defer resp.Body.Close()
	kind := resp.Header.Get("Content-Type")
	if resp.StatusCode != 200 {
		http.Error(w, "image unavailable", resp.StatusCode)
		return
	}
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		http.Error(w, "unsupported image", 415)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 26<<20))
}
