package controller

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

// TestControllerUIServesEveryReferencedAsset guards against pages that load
// stylesheets, scripts, fonts or images the controller does not serve.
func TestControllerUIServesEveryReferencedAsset(t *testing.T) {
	handler := NewServer(nil, provider.NewRegistry()).Handler()
	get := func(target string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		return response
	}
	entries, err := fs.ReadDir(controllerUI, "web")
	if err != nil {
		t.Fatal(err)
	}
	reference := regexp.MustCompile(`(?:href|src)="(/[^"?#]+\.[a-z0-9]+)"|url\((/[^)?#]+\.[a-z0-9]+)\)|["'](/[a-z0-9-]+\.(?:css|js|png|svg|woff2))["']`)
	checked := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		ext := path.Ext(name)
		if ext != ".html" && ext != ".css" && ext != ".js" {
			continue
		}
		if ext == ".js" && (strings.HasPrefix(name, "xterm") || strings.HasPrefix(name, "novnc") || name == "motion.js") {
			continue
		}
		data, err := fs.ReadFile(controllerUI, "web/"+name)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range reference.FindAllStringSubmatch(string(data), -1) {
			target := match[1] + match[2] + match[3]
			if checked[target] || strings.HasPrefix(target, "/v1/") {
				continue
			}
			checked[target] = true
			if response := get(target); response.Code != http.StatusOK {
				t.Errorf("%s references %s: status=%d", name, target, response.Code)
			}
		}
	}
	if len(checked) == 0 {
		t.Fatal("no asset references found")
	}
	for _, entry := range entries {
		name := entry.Name()
		want, ok := uiAssetTypes[path.Ext(name)]
		if !ok {
			continue
		}
		response := get("/" + name)
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != want {
			t.Errorf("/%s status=%d content-type=%q", name, response.Code, response.Header().Get("Content-Type"))
		}
	}
	for _, target := range []string{"/missing.css", "/chat.html", "/web/chat.css", "/.hidden.js", "/v1/unknown.js"} {
		if response := get(target); response.Code == http.StatusOK {
			t.Errorf("%s unexpectedly served", target)
		}
	}
}
