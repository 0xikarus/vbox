package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/procexec"
)

func testLocalChromiumProfile(t *testing.T) (string, localBrowserProfile) {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, ".config", "chromium")
	path := filepath.Join(root, "Profile 2")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "Preferences"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	state := `{"profile":{"info_cache":{"Profile 2":{"name":"Research"}}}}`
	if err := os.WriteFile(filepath.Join(root, "Local State"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	return home, localBrowserProfile{Browser: "chromium", Name: "Research", Path: path, UserDataDir: root, ProfileDir: "Profile 2"}
}

func TestDiscoverLocalBrowserProfilesShowsFullPath(t *testing.T) {
	home, want := testLocalChromiumProfile(t)
	got := discoverLocalBrowserProfiles(map[string]string{"HOME": home})
	if len(got) != 1 || got[0] != want {
		t.Fatalf("discovered profiles = %+v", got)
	}
}

func TestDiscoverWindowsChromeProfileThroughWSLPath(t *testing.T) {
	if !browserInWSL(map[string]string{"WSL_INTEROP": "synthetic"}) {
		t.Skip("WSL path conversion is a Linux CLI path")
	}
	home := t.TempDir()
	local := filepath.Join(home, "windows-local")
	profile := filepath.Join(local, "Google", "Chrome", "User Data", "Default")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "Preferences"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "wslpath"), []byte("#!/bin/sh\nprintf '%s\\n' \"$VMBOX_TEST_WSL_LOCAL\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VMBOX_TEST_WSL_LOCAL", local)
	got := discoverLocalBrowserProfiles(map[string]string{"HOME": home, "WSL_INTEROP": "synthetic", "LocalAppData": `C:\Users\synthetic\AppData\Local`})
	if len(got) != 1 || got[0].Browser != "chrome" || got[0].Path != profile {
		t.Fatalf("Windows Chrome path not discovered through WSL: %+v", got)
	}
}

func TestProfileUploadDialogSavesMarkedBrowserProfile(t *testing.T) {
	home, profile := testLocalChromiumProfile(t)
	const syntheticState = `{"version":2,"origins":[{"origin":"https://example.test","cookies":[{"name":"session","value":"synthetic-private","path":"/","domain":".example.test"}]}]}`
	var upload v1.SaveBrowserProfileRequest
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/login-profiles":
			_ = json.NewEncoder(w).Encode([]v1.LoginProfile{})
		case "GET /v1/browser-profiles":
			_ = json.NewEncoder(w).Encode([]v1.BrowserProfile{})
		case "PUT /v1/browser-profiles/chromium-profile-2":
			puts++
			if err := json.NewDecoder(r.Body).Decode(&upload); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(v1.BrowserProfile{Name: "chromium-profile-2"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := New()
	a.Environ = map[string]string{"HOME": home}
	a.Runner = &procexec.FakeRunner{}
	a.IsTerminal = func() bool { return true }
	a.browserExport = func(_ context.Context, got localBrowserProfile, _ func(string)) ([]byte, error) {
		if got != profile {
			t.Errorf("wrong browser profile: %+v", got)
		}
		return []byte(syntheticState), nil
	}
	var output, screen bytes.Buffer
	a.Out, a.Err = &output, &screen
	a.In = strings.NewReader(" " + strings.Repeat("\t", 4) + "\r")
	if err := a.controllerLoginProfiles(context.Background(), config.Context{Controller: server.URL}, "synthetic-token", []string{"upload"}); err != nil {
		t.Fatal(err)
	}
	if puts != 1 || upload.SourcePath != profile.Path || upload.SourceBrowser != "chromium" || string(upload.State) != syntheticState {
		t.Fatalf("browser upload mismatch: puts=%d browser=%s path=%s", puts, upload.SourceBrowser, upload.SourcePath)
	}
	if !strings.Contains(screen.String(), "Source path") || !strings.Contains(screen.String(), filepath.Base(profile.UserDataDir)) || !strings.Contains(screen.String(), "Profile 2") || !strings.Contains(output.String(), "Uploaded 1 profile") {
		t.Fatal("browser profile path or completion missing")
	}
}

func TestAttachSavedBrowserProfileThroughCLI(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/logical-boxes/disposable-box/browser/profiles/chromium-default/attach":
			_, _ = w.Write([]byte(`{"id":"disposable-import"}`))
		case "/v1/logical-boxes/disposable-box/browser/imports/disposable-import/apply":
			_, _ = w.Write([]byte(`{"status":"applied"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := New()
	var out bytes.Buffer
	a.Out = &out
	if err := a.controllerLoginProfiles(context.Background(), config.Context{Controller: server.URL}, "synthetic-token", []string{"attach-browser", "chromium-default", "disposable-box", "--apply"}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0] != "POST /v1/logical-boxes/disposable-box/browser/profiles/chromium-default/attach" || requests[1] != "POST /v1/logical-boxes/disposable-box/browser/imports/disposable-import/apply" || !strings.Contains(out.String(), "Applied browser profile") {
		t.Fatalf("unexpected attach/apply flow: %+v", requests)
	}
}
