package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Explicit opt-in: real browser process, isolated profile and synthetic TLS site.
func TestChromiumStateAndPasswordIntegration(t *testing.T) {
	binary := os.Getenv("VMBOX_TEST_CHROMIUM")
	if binary == "" {
		t.Skip("set VMBOX_TEST_CHROMIUM for isolated real-browser test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	site := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<input id="pw" type="password"><input id="other"><script>window.submitted=false;</script>`)
	}))
	defer site.Close()
	profileRoot := t.TempDir()
	if root := os.Getenv("VMBOX_TEST_PROFILE_ROOT"); root != "" {
		var err error
		profileRoot, err = os.MkdirTemp(root, "desktop-browser-test-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(profileRoot) })
	}
	profile := filepath.Join(profileRoot, "profile")
	start := func() (*Client, *exec.Cmd) {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, "--headless", "--disable-background-networking", "--disable-sync", "--no-sandbox", "--ignore-certificate-errors", "--no-first-run", "--no-default-browser-check", "--user-data-dir="+profile, "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", "about:blank")
		cmd.Stdout = io.Discard
		var startupLog bytes.Buffer
		cmd.Stderr = &startupLog
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				cmd.Process.Kill()
				cmd.Wait()
			}
		})
		for i := 0; i < 100; i++ {
			if c, err := Connect(ctx, profile); err == nil {
				return c, cmd
			}
			select {
			case <-ctx.Done():
				t.Fatal("browser startup timeout")
			case <-time.After(50 * time.Millisecond):
			}
		}
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("isolated browser unavailable: %s", startupLog.String())
		return nil, nil
	}
	closeBrowser := func(c *Client, cmd *exec.Cmd) {
		t.Helper()
		_ = c.Call(ctx, "", "Browser.close", nil, nil)
		c.Close()
		if err := cmd.Wait(); err != nil {
			t.Fatal("browser shutdown failed")
		}
	}
	client, cmd := start()
	expiry := float64(time.Now().Add(time.Hour).Unix())
	state := StateImport{Version: 1, Origins: []OriginState{{Origin: site.URL, Cookies: []StateCookie{{Name: "session", Value: "synthetic-cookie", Path: "/", HTTPOnly: true, Expires: &expiry}}, LocalStorage: []StorageEntry{{Name: "token", Value: "synthetic-storage"}}}}}
	if err := client.ApplyState(ctx, state); err != nil {
		t.Fatal(err)
	}
	closeBrowser(client, cmd)
	client, cmd = start()
	defer closeBrowser(client, cmd)
	var cookies struct {
		Cookies []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"cookies"`
	}
	if err := client.Call(ctx, "", "Storage.getCookies", nil, &cookies); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range cookies.Cookies {
		if c.Name == "session" && c.Value == "synthetic-cookie" {
			found = true
		}
	}
	if !found {
		t.Fatal("cookie did not survive restart")
	}
	var target struct {
		ID string `json:"targetId"`
	}
	if err := client.Call(ctx, "", "Target.createTarget", map[string]any{"url": site.URL}, &target); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(ctx, "", "Target.activateTarget", map[string]any{"targetId": target.ID}, nil); err != nil {
		t.Fatal(err)
	}
	var session struct {
		ID string `json:"sessionId"`
	}
	if err := client.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.ID, "flatten": true}, &session); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		var ready runtimeResult
		if err := client.Call(ctx, session.ID, "Runtime.evaluate", map[string]any{"expression": "!!document.querySelector('#pw')", "returnByValue": true}, &ready); err == nil && string(ready.Result.Value) == "true" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	var value runtimeResult
	if err := client.Call(ctx, session.ID, "Runtime.evaluate", map[string]any{"expression": "localStorage.getItem('token')", "returnByValue": true}, &value); err != nil {
		t.Fatal(err)
	}
	var storage string
	json.Unmarshal(value.Result.Value, &storage)
	if storage != "synthetic-storage" {
		t.Fatal("localStorage did not survive restart")
	}
	if err := client.Call(ctx, session.ID, "Runtime.evaluate", map[string]any{"expression": "document.querySelector('#pw').focus()"}, nil); err != nil {
		t.Fatal(err)
	}
	field, err := client.FocusedPassword(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.InsertPassword(ctx, field, "https://wrong.test", []byte("synthetic-password")); err == nil {
		t.Fatal("wrong origin accepted")
	}
	if err = client.InsertPassword(ctx, field, site.URL, []byte("synthetic-password")); err != nil {
		t.Fatal(err)
	}
	if err = client.Call(ctx, session.ID, "Runtime.evaluate", map[string]any{"expression": "document.querySelector('#pw').value === 'synthetic-password'", "returnByValue": true}, &value); err != nil || string(value.Result.Value) != "true" {
		t.Fatal("password not inserted")
	}
	if err = client.Call(ctx, session.ID, "Runtime.evaluate", map[string]any{"expression": "document.querySelector('#other').focus()"}, nil); err != nil {
		t.Fatal(err)
	}
	if err = client.InsertPassword(ctx, field, site.URL, []byte("synthetic-password")); err == nil {
		t.Fatal("focus change accepted")
	}
}
