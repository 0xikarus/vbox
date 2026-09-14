package browser

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEndpointStaysOnLoopback(t *testing.T) {
	for _, value := range []string{"9222\n/devtools/browser/test", "0\n/devtools/browser/test", "9222\nws://elsewhere/private", "9222\n/devtools/browser/test?token=private", "9222\n/devtools/browser/", "65536\n/devtools/browser/test"} {
		profile := t.TempDir()
		if err := os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		endpoint, err := Endpoint(profile)
		valid := value == "9222\n/devtools/browser/test"
		if (err == nil) != valid {
			t.Fatalf("endpoint validation: %v", err)
		}
		if valid && endpoint != "ws://127.0.0.1:9222/devtools/browser/test" {
			t.Fatal("endpoint escaped loopback")
		}
	}
}

func TestCDPMatchesResponseAndSanitizesRemoteErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		for i := 0; i < 2; i++ {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var req struct {
				ID uint64 `json:"id"`
			}
			if json.Unmarshal(data, &req) != nil {
				return
			}
			conn.Write(ctx, websocket.MessageText, []byte(`{"method":"Runtime.consoleAPICalled","params":{"value":"synthetic-private"}}`))
			var reply []byte
			if i == 0 {
				reply, _ = json.Marshal(map[string]any{"id": req.ID, "result": map[string]any{"ready": true}})
			} else {
				reply, _ = json.Marshal(map[string]any{"id": req.ID, "error": map[string]any{"message": "synthetic-private"}})
			}
			conn.Write(ctx, websocket.MessageText, reply)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{conn: conn}
	defer client.Close()
	var result struct {
		Ready bool `json:"ready"`
	}
	if err = client.Call(ctx, "", "test", nil, &result); err != nil || !result.Ready {
		t.Fatalf("response matching: %v", err)
	}
	if err = client.Call(ctx, "", "test", nil, nil); err == nil || strings.Contains(err.Error(), "synthetic-private") {
		t.Fatal("remote error not sanitized")
	}
}
