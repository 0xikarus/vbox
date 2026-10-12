package cli

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/0xikarus/vmbox-service/internal/browser"
)

//go:embed browser_export_extension/*
var browserExportExtension embed.FS

func installBrowserExportExtension(env map[string]string, profile localBrowserProfile) (string, error) {
	config := browserEnvValue(env, "XDG_CONFIG_HOME")
	if runtime.GOOS == "windows" {
		config = browserEnvValue(env, "APPDATA")
	}
	if browserInWSL(env) && strings.HasPrefix(profile.UserDataDir, "/mnt/") {
		config = browserLocalAppData(env)
	}
	if config == "" {
		home := browserEnvValue(env, "HOME")
		if home == "" {
			home = browserEnvValue(env, "USERPROFILE")
		}
		if home == "" {
			return "", fmt.Errorf("home directory unavailable for browser export helper")
		}
		config = filepath.Join(home, ".config")
	}
	dir := filepath.Join(config, "vbox", "chromium-export-extension")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	for _, name := range []string{"manifest.json", "bridge.js", "background.js"} {
		data, err := browserExportExtension.ReadFile("browser_export_extension/" + name)
		if err != nil {
			return "", err
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			return "", err
		}
	}
	if browserInWSL(env) && strings.HasPrefix(profile.UserDataDir, "/mnt/") {
		if windowsPath := wslPath("-w", dir); windowsPath != "" {
			return windowsPath, nil
		}
	}
	return dir, nil
}

func browserExecutable(env map[string]string, profile localBrowserProfile) (string, error) {
	browserName := profile.Browser
	key := "VMBOX_CHROME_BINARY"
	if browserName == "chromium" {
		key = "VMBOX_CHROMIUM_BINARY"
	}
	if value := browserEnvValue(env, key); value != "" {
		if browserInWSL(env) && len(value) >= 2 && value[1] == ':' {
			value = wslPath("-u", value)
		}
		if info, err := os.Stat(value); err == nil && info.Mode().IsRegular() {
			return value, nil
		}
		return "", fmt.Errorf("%s does not point to a browser executable", key)
	}
	if browserInWSL(env) && strings.HasPrefix(profile.UserDataDir, "/mnt/") {
		if path := wslBrowserExecutable(env, browserName); path != "" {
			return path, nil
		}
		return "", fmt.Errorf("Windows %s executable unavailable; set %s to its full path", browserName, key)
	}
	names := []string{"google-chrome-stable", "google-chrome", "chrome"}
	switch filepath.Base(profile.UserDataDir) {
	case "google-chrome-beta":
		names = []string{"google-chrome-beta"}
	case "google-chrome-unstable":
		names = []string{"google-chrome-unstable"}
	}
	if browserName == "chromium" {
		names = []string{"chromium", "chromium-browser"}
	}
	for _, name := range names {
		if binary, err := exec.LookPath(name); err == nil {
			return binary, nil
		}
	}
	if runtime.GOOS == "windows" {
		roots := []string{browserEnvValue(env, "PROGRAMFILES"), browserEnvValue(env, "PROGRAMFILES(X86)"), browserEnvValue(env, "LOCALAPPDATA")}
		relative := filepath.Join("Google", "Chrome", "Application", "chrome.exe")
		if browserName == "chromium" {
			relative = filepath.Join("Chromium", "Application", "chrome.exe")
		}
		for _, root := range roots {
			if root == "" {
				continue
			}
			path := filepath.Join(root, relative)
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("%s executable unavailable; set %s to its full path", browserName, key)
}

func wslBrowserExecutable(env map[string]string, browserName string) string {
	local := browserLocalAppData(env)
	relative := filepath.Join("Google", "Chrome", "Application", "chrome.exe")
	if browserName == "chromium" {
		relative = filepath.Join("Chromium", "Application", "chrome.exe")
	}
	if local != "" {
		path := filepath.Join(local, relative)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	for _, windowsRoot := range []string{`C:\Program Files`, `C:\Program Files (x86)`} {
		root := wslPath("-u", windowsRoot)
		if root == "" {
			continue
		}
		path := filepath.Join(root, relative)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

func launchBrowserProfile(env map[string]string, profile localBrowserProfile, url string) error {
	binary, err := browserExecutable(env, profile)
	if err != nil {
		return err
	}
	userDataDir := profile.UserDataDir
	if browserInWSL(env) && strings.HasSuffix(strings.ToLower(binary), ".exe") {
		userDataDir = wslPath("-w", userDataDir)
		if userDataDir == "" {
			return fmt.Errorf("could not convert selected Windows browser path")
		}
	}
	cmd := exec.Command(binary, "--user-data-dir="+userDataDir, "--profile-directory="+profile.ProfileDir, url)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not open selected browser profile: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func browserExportPage(profile localBrowserProfile, extensionPath string) string {
	return `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="referrer" content="no-referrer"><title>vbox browser export</title>
<style>body{font:16px system-ui;max-width:680px;margin:8vh auto;padding:0 20px;line-height:1.5}code{overflow-wrap:anywhere}button{font:inherit;padding:12px 18px;margin:16px 0}p{color:#444}</style>
<h1>Export browser sessions to vbox</h1><p>Selected profile: <strong>` + html.EscapeString(profile.Browser+" / "+profile.Name) + `</strong><br><code>` + html.EscapeString(profile.Path) + `</code></p>
<p>The export includes HTTPS cookies and localStorage from open HTTPS tabs. It is saved encrypted on your vbox controller for later use in a box. Browser passwords, bookmarks, extensions, and passkeys are excluded.</p>
<button id="export-browser-profile" disabled>Export and upload</button><p id="export-status">Waiting for the vbox browser export extension. If this button stays disabled, open <code>chrome://extensions</code>, enable Developer mode, choose Load unpacked, and select <code>` + html.EscapeString(extensionPath) + `</code>. Then reload this tab.</p></html>`
}

type browserExportBridge struct {
	url      string
	received <-chan []byte
	close    func()
}

func startBrowserExportBridge(profile localBrowserProfile, extensionPath string) (browserExportBridge, error) {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return browserExportBridge{}, err
	}
	token := hex.EncodeToString(nonce[:])
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return browserExportBridge{}, fmt.Errorf("local browser export bridge unavailable: %w", err)
	}
	path := "/vbox-browser-export/" + token
	base := "http://" + listener.Addr().String()
	dataCh := make(chan []byte, 1)
	var accepted atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; connect-src 'self'")
		io.WriteString(w, browserExportPage(profile, extensionPath))
	})
	mux.HandleFunc("POST "+path+"/ingest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Origin") != base || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "export origin unavailable", 403)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, browser.MaxStateBytes))
		if err != nil {
			http.Error(w, "browser export exceeds 8 MiB", 413)
			return
		}
		if _, err := browser.DecodeStateImport(body); err != nil {
			clear(body)
			http.Error(w, "browser export contains unsupported state", 400)
			return
		}
		if !accepted.CompareAndSwap(false, true) {
			clear(body)
			http.Error(w, "browser export already received", 409)
			return
		}
		select {
		case dataCh <- body:
			w.WriteHeader(202)
		case <-r.Context().Done():
			clear(body)
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return browserExportBridge{url: base + path, received: dataCh, close: func() { _ = server.Close() }}, nil
}

func (a *App) exportLocalBrowserProfile(ctx context.Context, profile localBrowserProfile, progress func(string)) ([]byte, error) {
	extensionPath, err := installBrowserExportExtension(a.Environ, profile)
	if err != nil {
		return nil, err
	}
	bridge, err := startBrowserExportBridge(profile, extensionPath)
	if err != nil {
		return nil, err
	}
	defer bridge.close()
	progress("Open the selected browser profile; load the helper if prompted, then click Export and upload…")
	if err := launchBrowserProfile(a.Environ, profile, bridge.url); err != nil {
		return nil, err
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	select {
	case data := <-bridge.received:
		return data, nil
	case <-wait.Done():
		return nil, fmt.Errorf("browser export timed out or was cancelled; helper path: %s", extensionPath)
	}
}
