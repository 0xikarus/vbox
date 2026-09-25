package boxruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestCodexTUIProxyTracksStartResumeAndDisconnect(t *testing.T) {
	session := fmt.Sprintf("codex-proxy-test-%d", time.Now().UnixNano())
	backend, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", CodexChatPort(session)))
	if err != nil {
		t.Fatal(err)
	}
	var starts atomic.Int64
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		for {
			_, raw, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var request struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Error(err)
				return
			}
			threadID := "first-thread"
			ephemeral := false
			if request.Method == "thread/start" && starts.Add(1) > 1 {
				threadID = "auxiliary-thread"
				ephemeral = true
			}
			if request.Method == "thread/resume" {
				threadID = "resumed-thread"
			}
			response, _ := json.Marshal(map[string]any{"id": request.ID, "result": map[string]any{"thread": map[string]any{"id": threadID, "ephemeral": ephemeral}}})
			if err := conn.Write(r.Context(), websocket.MessageText, response); err != nil {
				return
			}
		}
	})}
	go func() { _ = server.Serve(backend) }()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	var done atomic.Bool
	go func() { _ = ServeCodexTUIProxy(ctx, root, session); done.Store(true) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, _, err := codexTUIProxyState(context.Background(), session)
		if err == nil {
			break
		}
		if done.Load() || time.Now().After(deadline) {
			t.Fatalf("Codex TUI proxy did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	conn, _, err := websocket.Dial(context.Background(), codexTUIProxyURL(session), nil)
	if err != nil {
		t.Fatal(err)
	}
	for index, method := range []string{"thread/start", "thread/start", "thread/resume"} {
		request, _ := json.Marshal(map[string]any{"id": index + 1, "method": method, "params": map[string]any{}})
		if err := conn.Write(context.Background(), websocket.MessageText, request); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.Read(context.Background()); err != nil {
			t.Fatal(err)
		}
		connected, threadID, err := codexTUIProxyState(context.Background(), session)
		want := "first-thread"
		if method == "thread/resume" {
			want = "resumed-thread"
		}
		if err != nil || !connected || threadID != want {
			t.Fatalf("after %s: connected=%t thread=%q err=%v", method, connected, threadID, err)
		}
		if index == 0 {
			auxiliary, _, err := websocket.Dial(context.Background(), codexTUIProxyURL(session), nil)
			if err != nil {
				t.Fatal(err)
			}
			_ = auxiliary.Close(websocket.StatusNormalClosure, "")
			connected, threadID, err := codexTUIProxyState(context.Background(), session)
			if err != nil || !connected || threadID != want {
				t.Fatalf("auxiliary connection changed selection: connected=%t thread=%q err=%v", connected, threadID, err)
			}
		}
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	for time.Now().Before(deadline.Add(2 * time.Second)) {
		connected, threadID, err := codexTUIProxyState(context.Background(), session)
		if err == nil && !connected && threadID == "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("proxy retained a thread after the TUI disconnected")
}
