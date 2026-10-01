package boxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestCodexRecoveryWaitsForTurnThenQueuesExactInboxOnce(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	session, thread, messageID := "codex-recover", "visible-thread", "message-recover"
	if err := StoreChatInbound(home, session, ChatInbound{ID: messageID, Text: "inspect the latest changes"}); err != nil {
		t.Fatal(err)
	}
	if err := bindCodexMessageThread(root, session, messageID, thread); err != nil {
		t.Fatal(err)
	}
	inbox := filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", session, messageID+".json")
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(inbox, old, old); err != nil {
		t.Fatal(err)
	}
	var active atomic.Bool
	active.Store(true)
	var adds, starts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				ID     int64          `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Error(err)
				return
			}
			var result map[string]any
			switch request.Method {
			case "thread/turns/list":
				status := "interrupted"
				if active.Load() {
					status = "inProgress"
				}
				result = map[string]any{"data": []any{map[string]any{"status": status}}}
			case "thread/queue/list":
				items := []any{}
				if adds.Load() > starts.Load() {
					items = append(items, map[string]any{"id": "queued-1"})
				}
				result = map[string]any{"data": items}
			case "thread/queue/add":
				input, _ := request.Params["input"].([]any)
				if len(input) != 1 {
					t.Errorf("wrong input count: %v", request.Params)
					return
				}
				first, _ := input[0].(map[string]any)
				if request.Params["threadId"] != thread || request.Params["clientUserMessageId"] != messageID || first["text"] != "inspect the latest changes" {
					t.Errorf("replayed wrong native input: %v", request.Params)
				}
				adds.Add(1)
				result = map[string]any{"queuedSubmission": map[string]any{"id": "queued-1"}}
			case "thread/queue/start":
				if request.Params["queuedSubmissionId"] != "queued-1" {
					t.Errorf("started wrong native item: %v", request.Params)
				}
				starts.Add(1)
				result = map[string]any{}
			default:
				t.Errorf("unexpected method %q", request.Method)
				return
			}
			response, _ := json.Marshal(map[string]any{"id": request.ID, "result": result})
			if err := conn.Write(r.Context(), websocket.MessageText, response); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &codexClient{conn: conn}
	defer client.Close()
	if err := recoverUnconsumedCodexInbox(context.Background(), client, root, home, session, thread, messageID); err != nil || adds.Load() != 0 {
		t.Fatalf("message was replayed during active turn: adds=%d error=%v", adds.Load(), err)
	}
	active.Store(false)
	for range 2 {
		if err := recoverUnconsumedCodexInbox(context.Background(), client, root, home, session, thread, messageID); err != nil {
			t.Fatal(err)
		}
	}
	if adds.Load() != 1 || starts.Load() != 1 {
		t.Fatalf("recovery submitted %d and started %d times, want one each", adds.Load(), starts.Load())
	}
	if _, err := os.Stat(inbox); err != nil {
		t.Fatalf("recovery removed inbox before native receipt: %v", err)
	}
}

func TestCodexMessageThreadBindingRejectsThreadSwitch(t *testing.T) {
	root := t.TempDir()
	if err := bindCodexMessageThread(root, "codex-session", "message-1", "first-thread"); err != nil {
		t.Fatal(err)
	}
	if err := bindCodexMessageThread(root, "codex-session", "message-1", "second-thread"); !errors.Is(err, ErrAmbiguousMessage) {
		t.Fatalf("changed target thread was accepted: %v", err)
	}
}
