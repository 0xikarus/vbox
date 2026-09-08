package controller

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// factoryGateway keeps browser authentication on the controller. The separate
// factory service trusts only its dedicated gateway token, never client headers.
func (s *Server) factoryGateway(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	if s.FactoryURL == "" || s.FactoryToken == "" {
		if r.Method == "GET" && r.URL.Path == "/v1/factory/capabilities" {
			writeJSON(w, 200, map[string]any{"enabled": false, "githubConfigured": false, "agents": []any{}})
			return
		}
		writeError(w, 503, fmt.Errorf("factory is not enabled on this controller"))
		return
	}
	// Native access and saved-profile provisioning are owner-only today. Do not
	// expand that authority indirectly through a planning request.
	if p.Role != "owner" {
		writeError(w, 403, fmt.Errorf("factory currently requires the account owner role"))
		return
	}
	target, err := url.Parse(s.FactoryURL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") {
		writeError(w, 503, fmt.Errorf("invalid factory backend configuration"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 11<<20)
	proxy := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		// Start from a small allowlist: cookies and untrusted identity/forwarding
		// headers must never reach the backend or a redirected upstream.
		h := make(http.Header)
		for _, key := range []string{"Content-Type", "Accept", "Idempotency-Key", "If-Match"} {
			if value := pr.In.Header.Get(key); value != "" {
				h.Set(key, value)
			}
		}
		h.Set("Authorization", "Bearer "+s.FactoryToken)
		h.Set("X-Vmbox-Account", p.AccountID)
		h.Set("X-Vmbox-User", p.UserID)
		pr.Out.Header = h
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		writeError(w, 502, fmt.Errorf("factory unavailable; an accepted operation may still be running; refresh before retrying"))
	}, ModifyResponse: func(resp *http.Response) error {
		resp.Header.Del("Set-Cookie")
		resp.Header.Del("WWW-Authenticate")
		if location := resp.Header.Get("Location"); location != "" && !strings.HasPrefix(location, "/v1/factory/") {
			return fmt.Errorf("factory redirect rejected")
		}
		return nil
	}}
	proxy.ServeHTTP(w, r)
}
