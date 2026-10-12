package cli

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserExportHelperInstallsIntoDisposableConfig(t *testing.T) {
	home := t.TempDir()
	path, err := installBrowserExportExtension(map[string]string{"HOME": home}, localBrowserProfile{Browser: "chromium"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.json", "background.js", "bridge.js"} {
		data, err := os.ReadFile(filepath.Join(path, name))
		if err != nil || len(data) == 0 {
			t.Fatalf("browser helper file %s unavailable", name)
		}
	}
}

func TestLocalBrowserExportBridgeAcceptsOnlyBoundValidStateOnce(t *testing.T) {
	profile := localBrowserProfile{Browser: "chromium", Name: "disposable", Path: "/tmp/disposable-profile"}
	bridge, err := startBrowserExportBridge(profile, "/tmp/disposable-extension")
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.close()
	client := &http.Client{}
	page, err := client.Get(bridge.url)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != 200 || !strings.Contains(string(content), profile.Path) || page.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("local export page missing selected profile or privacy header")
	}
	parsed, _ := url.Parse(bridge.url)
	origin := parsed.Scheme + "://" + parsed.Host
	const state = `{"version":2,"origins":[{"origin":"https://example.test","cookies":[{"name":"session","value":"synthetic-private","path":"/"}]}]}`
	post := func(value, requestOrigin string) int {
		t.Helper()
		request, err := http.NewRequest("POST", bridge.url+"/ingest", strings.NewReader(value))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		if requestOrigin != "" {
			request.Header.Set("Origin", requestOrigin)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if got := post(state, ""); got != 403 {
		t.Fatalf("missing origin status %d", got)
	}
	if got := post(state, "http://other.test"); got != 403 {
		t.Fatalf("foreign origin status %d", got)
	}
	if got := post(`{"version":2,"origins":[]}`, origin); got != 400 {
		t.Fatalf("invalid state status %d", got)
	}
	if got := post(state, origin); got != 202 {
		t.Fatalf("valid state status %d", got)
	}
	if got := post(state, origin); got != 409 {
		t.Fatalf("duplicate state status %d", got)
	}
	if got := string(<-bridge.received); got != state {
		t.Fatal("local bridge changed exported state")
	}
}
