package boxruntime

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testMCPActivityConfig(t *testing.T, controller string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	data, _ := json.Marshal(DesktopAgentConfig{Controller: controller, Assignment: "assignment", Token: strings.Repeat("a", 64)})
	if err := writeTextAtomic(filepath.Join(home, ".config", "vmbox", "desktop-agent.json"), string(data), 0600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestMCPActivityQueueOmitsOwnRepliesAndArguments(t *testing.T) {
	home := testMCPActivityConfig(t, "https://example.invalid")
	if err := queueLocalMCPActivity("assignment", "chat_message", json.RawMessage(`{"replyTo":"private-ref","text":"private reply"}`), nil); err != nil {
		t.Fatal(err)
	}
	if err := queueLocalMCPActivity("assignment", "chat_message", json.RawMessage(`{"contact":"reviewer","text":"private contact text"}`), nil); err != nil {
		t.Fatal(err)
	}
	if err := queueLocalMCPActivity("assignment", "take_screenshot", json.RawMessage(`{"output":"/private/path"}`), nil); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(localMCPActivityDir(home))
	if err != nil || len(entries) != 2 {
		t.Fatalf("queued entries=%v err=%v", entries, err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(localMCPActivityDir(home), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "private") || strings.Contains(string(data), "reviewer") {
			t.Fatalf("MCP arguments leaked into activity: %s", data)
		}
	}
}

func TestMCPActivityHTTPCallQueuesBullet(t *testing.T) {
	home := testMCPActivityConfig(t, "https://example.invalid")
	handler := desktopMCPHTTPHandler("assignment", "local-test-token", allDesktopToolPolicy)
	status, response := desktopMCPHTTPRequest(t, handler, http.MethodPost, "/tools/heartbeat", "local-test-token", `{"action":"stop"}`)
	if status != http.StatusOK {
		t.Fatalf("heartbeat stop status=%d response=%v", status, response)
	}
	entries, err := os.ReadDir(localMCPActivityDir(home))
	if err != nil || len(entries) != 1 {
		t.Fatalf("queued entries=%v err=%v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(localMCPActivityDir(home), entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var activity MCPActivity
	if err := json.Unmarshal(data, &activity); err != nil || activity.Tool != "heartbeat" || activity.Action != "stop" || activity.Failed {
		t.Fatalf("queued activity=%+v err=%v", activity, err)
	}
}

func TestMCPActivityDrainKeepsUnconfirmedEventThenAcknowledges(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/agent-desktop/tool-activity" || r.Header.Get("Authorization") != "DesktopAgent "+strings.Repeat("a", 64) {
			t.Errorf("unexpected activity request: %s", r.URL.Path)
		}
		if requests == 1 {
			http.Error(w, "try again", http.StatusServiceUnavailable)
			return
		}
		var activity MCPActivity
		if err := json.NewDecoder(r.Body).Decode(&activity); err != nil || activity.Tool != "heartbeat" || activity.Action != "stop" {
			t.Errorf("activity=%+v err=%v", activity, err)
		}
		_, _ = w.Write([]byte(`{"stored":true}`))
	}))
	defer server.Close()
	cert := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", cert)
	home := testMCPActivityConfig(t, server.URL)
	if err := queueLocalMCPActivity("assignment", "heartbeat", json.RawMessage(`{"action":"stop"}`), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localMCPActivityDir(home), "broken.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := drainLocalMCPActivity(context.Background(), "assignment", home); err == nil {
		t.Fatal("unconfirmed activity was acknowledged")
	}
	if entries, _ := os.ReadDir(localMCPActivityDir(home)); len(entries) != 2 {
		t.Fatalf("queued entries after failure=%d", len(entries))
	}
	if err := drainLocalMCPActivity(context.Background(), "assignment", home); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(localMCPActivityDir(home)); len(entries) != 1 || entries[0].Name() != "broken.json.invalid" {
		t.Fatalf("queued entries after acknowledgement=%v", entries)
	}
}
