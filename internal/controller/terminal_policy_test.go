package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTerminalPagesAllowGeneratedStylesNotInlineScripts(t *testing.T) {
	for _, path := range []string{"/", "/grid", "/boxes/example"} {
		r := httptest.NewRecorder()
		securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		csp := r.Header().Get("Content-Security-Policy")
		if strings.Contains(csp, "style-src 'self' 'unsafe-inline'") != (path != "/") {
			t.Fatalf("incorrect terminal style policy for %s: %s", path, csp)
		}
		if !strings.Contains(csp, "script-src 'self';") {
			t.Fatalf("inline scripts allowed for %s", path)
		}
	}
}
