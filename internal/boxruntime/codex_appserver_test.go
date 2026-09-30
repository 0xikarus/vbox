package boxruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestEnsureCodexAppServerReplacesInterruptedHelper(t *testing.T) {
	originalCommand, originalReady := tmuxCommand, CodexAppServerReady
	t.Cleanup(func() { tmuxCommand, CodexAppServerReady = originalCommand, originalReady })
	stale, ready, killed := true, false, false
	CodexAppServerReady = func(context.Context, string) (bool, error) { return ready, nil }
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "has-session":
			if stale {
				return nil, nil
			}
			return nil, errors.New("no sessions")
		case "display-message":
			return []byte("bash\n"), nil
		case "kill-session":
			stale, killed = false, true
			return nil, nil
		case "new-session":
			ready = true
			return nil, nil
		default:
			t.Fatalf("unexpected tmux command: %v", args)
			return nil, nil
		}
	}
	if err := EnsureCodexAppServer(context.Background(), "codex-test"); err != nil {
		t.Fatal(err)
	}
	if !killed || !ready {
		t.Fatalf("stale helper was not replaced: killed=%t ready=%t", killed, ready)
	}
}

func TestCodexStartTurnStartsFreshThreadOnlyWhenOldOneIsMissing(t *testing.T) {
	turns, fresh := 0, 0
	err := codexStartTurnWithThreadRecovery("hello", nil, func([]map[string]any) error {
		turns++
		if turns == 1 {
			return errors.New("codex app server: thread not found: old-id")
		}
		return nil
	}, func() error { fresh++; return nil })
	if err != nil || turns != 2 || fresh != 1 {
		t.Fatalf("recovery: turns=%d fresh=%d err=%v", turns, fresh, err)
	}
	turns, fresh = 0, 0
	err = codexStartTurnWithThreadRecovery("hello", nil, func([]map[string]any) error {
		turns++
		return errors.New("turn is already running")
	}, func() error { fresh++; return nil })
	if err == nil || turns != 1 || fresh != 0 {
		t.Fatalf("unrelated failure retried: turns=%d fresh=%d err=%v", turns, fresh, err)
	}
}

func TestCodexCurrentThreadUsesVisibleBindingInsteadOfPersistedRecency(t *testing.T) {
	root := t.TempDir()
	if err := writeTextAtomic(codexResetPendingFile(root, "session"), "old-thread\n", 0600); err != nil {
		t.Fatal(err)
	}
	originalState := codexVisibleThreadState
	t.Cleanup(func() { codexVisibleThreadState = originalState })
	codexVisibleThreadState = func(context.Context, string) (bool, string, error) { return true, "visible-thread", nil }
	id, err := CodexCurrentThread(context.Background(), nil, root, "session", "/workspace")
	if err != nil || id != "visible-thread" {
		t.Fatalf("thread=%q err=%v", id, err)
	}
}

func TestMarkFreshCodexTUIInvalidatesOldThread(t *testing.T) {
	root := t.TempDir()
	if err := rememberCodexThread(root, "codex-wake", "old-thread"); err != nil {
		t.Fatal(err)
	}
	if err := markFreshCodexTUI(root, "codex-wake"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(codexResetPendingFile(root, "codex-wake")); err != nil || strings.TrimSpace(string(data)) != "visible-tui" {
		t.Fatalf("fresh TUI marker = %q, %v", data, err)
	}
	if _, err := os.Stat(codexThreadFile(root, "codex-wake")); !os.IsNotExist(err) {
		t.Fatalf("old thread binding survived wake: %v", err)
	}
}

func TestCodexCurrentThreadRejectsUnmaterializedTUI(t *testing.T) {
	root := t.TempDir()
	originalState := codexVisibleThreadState
	t.Cleanup(func() { codexVisibleThreadState = originalState })
	codexVisibleThreadState = func(context.Context, string) (bool, string, error) { return true, "", nil }
	if err := markFreshCodexTUI(root, "codex-wake"); err != nil {
		t.Fatal(err)
	}
	if _, err := CodexCurrentThread(context.Background(), nil, root, "codex-wake", "/workspace"); err == nil || !strings.Contains(err.Error(), "has not selected a thread") {
		t.Fatalf("unmaterialized TUI selected an old thread: %v", err)
	}
}

func TestWaitForCodexVisibleThreadUsesProxyBinding(t *testing.T) {
	root := t.TempDir()
	originalState := codexVisibleThreadState
	t.Cleanup(func() { codexVisibleThreadState = originalState })
	calls := 0
	codexVisibleThreadState = func(context.Context, string) (bool, string, error) {
		calls++
		if calls == 1 {
			return true, "", nil
		}
		return true, "fresh-visible-thread", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	thread, err := waitForCodexVisibleThread(ctx, nil, root, "codex-wake", "/workspace")
	if err != nil || thread != "fresh-visible-thread" || calls != 2 {
		t.Fatalf("thread=%q calls=%d err=%v", thread, calls, err)
	}
}

func TestCodexTurnInputUsesLocalImageSchema(t *testing.T) {
	got := codexTurnInput("inspect this", []string{"/tmp/first.png", "/tmp/second.jpg"})
	want := []map[string]any{
		{"type": "text", "text": "inspect this"},
		{"type": "localImage", "path": "/tmp/first.png"},
		{"type": "localImage", "path": "/tmp/second.jpg"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("turn input = %#v, want %#v", got, want)
	}
}

func TestCodexQueueInputSendsImageAsStructuredPart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept app-server client: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		_, data, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read queue request: %v", err)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
			Params struct {
				ThreadID            string           `json:"threadId"`
				ClientUserMessageID string           `json:"clientUserMessageId"`
				Input               []map[string]any `json:"input"`
			} `json:"params"`
		}
		if err := json.Unmarshal(data, &request); err != nil {
			t.Errorf("decode queue request: %v", err)
			return
		}
		if request.Method != "thread/queue/add" || request.Params.ThreadID != "visible-thread" || request.Params.ClientUserMessageID != "message-1" || !reflect.DeepEqual(request.Params.Input, codexTurnInput("inspect this", []string{"/tmp/scene.png"})) {
			t.Errorf("incorrect structured queue request: %+v", request)
		}
		response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"queuedSubmission": map[string]any{"id": "queued-1"}}})
		if err := conn.Write(r.Context(), websocket.MessageText, response); err != nil {
			t.Errorf("write queue response: %v", err)
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &codexClient{conn: conn}
	defer client.Close()
	if err := codexQueueInput(context.Background(), client, "visible-thread", "message-1", "inspect this", []string{"/tmp/scene.png"}); err != nil {
		t.Fatal(err)
	}
}

func TestCodexBusyTurnSteersChatWithExactMessageID(t *testing.T) {
	var methodsMu sync.Mutex
	var methods []string
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
			methodsMu.Lock()
			methods = append(methods, request.Method)
			methodsMu.Unlock()
			var result map[string]any
			switch request.Method {
			case "thread/turns/list":
				result = map[string]any{"data": []any{map[string]any{"id": "turn-1", "status": "inProgress"}}}
			case "turn/steer":
				if request.Params["threadId"] != "visible-thread" || request.Params["expectedTurnId"] != "turn-1" || request.Params["clientUserMessageId"] != "message-1" || !reflect.DeepEqual(request.Params["input"], []any{map[string]any{"type": "text", "text": "check this"}, map[string]any{"type": "localImage", "path": "/tmp/image.png"}}) {
					t.Errorf("incorrect steer request: %+v", request.Params)
				}
				result = map[string]any{"turnId": "turn-1"}
			default:
				t.Errorf("unexpected method %q", request.Method)
				return
			}
			response, _ := json.Marshal(map[string]any{"id": request.ID, "result": result})
			_ = conn.Write(r.Context(), websocket.MessageText, response)
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &codexClient{conn: conn}
	defer client.Close()
	if err := codexSubmitVisibleInput(context.Background(), client, "visible-thread", "message-1", "check this", []string{"/tmp/image.png"}); err != nil {
		t.Fatal(err)
	}
	methodsMu.Lock()
	defer methodsMu.Unlock()
	if !reflect.DeepEqual(methods, []string{"thread/turns/list", "turn/steer"}) {
		t.Fatalf("methods=%v", methods)
	}
}

func TestCodexSteerTurnEndRaceFallsBackToQueue(t *testing.T) {
	var methodsMu sync.Mutex
	var methods []string
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
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Error(err)
				return
			}
			methodsMu.Lock()
			methods = append(methods, request.Method)
			methodsMu.Unlock()
			response := map[string]any{"id": request.ID}
			switch request.Method {
			case "thread/turns/list":
				response["result"] = map[string]any{"data": []any{map[string]any{"id": "turn-1", "status": "inProgress"}}}
			case "turn/steer":
				response["error"] = map[string]any{"message": "no active turn to steer"}
			case "thread/queue/add":
				response["result"] = map[string]any{"queuedSubmission": map[string]any{"id": "queued-1"}}
			default:
				t.Errorf("unexpected method %q", request.Method)
				return
			}
			data, _ := json.Marshal(response)
			_ = conn.Write(r.Context(), websocket.MessageText, data)
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &codexClient{conn: conn}
	defer client.Close()
	if err := codexSubmitVisibleInput(context.Background(), client, "visible-thread", "message-1", "check this", nil); err != nil {
		t.Fatal(err)
	}
	methodsMu.Lock()
	defer methodsMu.Unlock()
	if !reflect.DeepEqual(methods, []string{"thread/turns/list", "turn/steer", "thread/queue/add"}) {
		t.Fatalf("methods=%v", methods)
	}
}

func TestCodexFreshThreadQueuesAndStartsFirstMessage(t *testing.T) {
	var methodsMu sync.Mutex
	var methods []string
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
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Error(err)
				return
			}
			methodsMu.Lock()
			methods = append(methods, request.Method)
			methodsMu.Unlock()
			response := map[string]any{"id": request.ID}
			switch request.Method {
			case "thread/turns/list":
				response["error"] = map[string]any{"message": "thread fresh-id is not materialized yet; thread/turns/list is unavailable before first user message"}
			case "thread/queue/add":
				response["result"] = map[string]any{"queuedSubmission": map[string]any{"id": "queued-1"}}
			case "thread/queue/list":
				response["result"] = map[string]any{"data": []any{map[string]any{"id": "queued-1"}}}
			case "thread/queue/start":
				response["result"] = map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}}
			default:
				t.Errorf("unexpected method %q", request.Method)
				return
			}
			data, _ := json.Marshal(response)
			_ = conn.Write(r.Context(), websocket.MessageText, data)
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &codexClient{conn: conn}
	defer client.Close()
	if err := codexSubmitVisibleInput(context.Background(), client, "fresh-id", "message-1", "hello", nil); err != nil {
		t.Fatal(err)
	}
	if err := codexStartQueuedIfIdle(context.Background(), client, "fresh-id"); err != nil {
		t.Fatal(err)
	}
	methodsMu.Lock()
	defer methodsMu.Unlock()
	if !reflect.DeepEqual(methods, []string{"thread/turns/list", "thread/queue/add", "thread/turns/list", "thread/queue/list", "thread/queue/start"}) {
		t.Fatalf("methods=%v", methods)
	}
}

func TestCodexQueueReceiptWaitsForMatchingNativeUserItem(t *testing.T) {
	var reads atomic.Int32
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
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(raw, &request)
			if request.Method != "thread/items/list" {
				t.Errorf("unexpected method %q", request.Method)
			}
			count := reads.Add(1)
			clientID := "other-message"
			if count > 1 {
				clientID = "expected-message"
			}
			response, _ := json.Marshal(map[string]any{"id": request.ID, "result": map[string]any{"data": []any{map[string]any{"item": map[string]any{"type": "userMessage", "clientId": clientID}}}}})
			_ = conn.Write(r.Context(), websocket.MessageText, response)
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &codexClient{conn: conn}
	defer client.Close()
	if err := waitForCodexUserItem(context.Background(), client, "visible-thread", "expected-message"); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 {
		t.Fatalf("confirmed after %d reads, wanted 2", reads.Load())
	}
}

func TestCodexQueueReceiptRestartsAfterActiveTurnIsInterrupted(t *testing.T) {
	originalPoll, originalRetry := codexUserItemPollInterval, codexQueueRetryInterval
	t.Cleanup(func() { codexUserItemPollInterval, codexQueueRetryInterval = originalPoll, originalRetry })
	codexUserItemPollInterval, codexQueueRetryInterval = 10*time.Millisecond, 20*time.Millisecond
	var checks, starts, turnChecks atomic.Int32
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
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Error(err)
				return
			}
			var result map[string]any
			switch request.Method {
			case "thread/items/list":
				checks.Add(1)
				result = map[string]any{"data": []any{}}
				if starts.Load() > 0 {
					result["data"] = []any{map[string]any{"item": map[string]any{"type": "userMessage", "clientId": "message-1"}}}
				}
			case "thread/turns/list":
				turnChecks.Add(1)
				status := "inProgress"
				if turnChecks.Load() > 1 {
					status = "interrupted"
				}
				result = map[string]any{"data": []any{map[string]any{"status": status}}}
			case "thread/queue/list":
				result = map[string]any{"data": []any{map[string]any{"id": "queued-1"}}}
			case "thread/queue/start":
				starts.Add(1)
				result = map[string]any{"turn": map[string]any{"status": "inProgress"}}
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForCodexUserItem(ctx, client, "visible-thread", "message-1"); err != nil {
		t.Fatal(err)
	}
	if checks.Load() < 3 || turnChecks.Load() < 2 || starts.Load() != 1 {
		t.Fatalf("checks=%d turnChecks=%d starts=%d", checks.Load(), turnChecks.Load(), starts.Load())
	}
}

func TestCodexIdleInterruptedQueueStartsOldestSubmission(t *testing.T) {
	var methodsMu sync.Mutex
	var methods []string
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
			methodsMu.Lock()
			methods = append(methods, request.Method)
			methodsMu.Unlock()
			var result map[string]any
			switch request.Method {
			case "thread/turns/list":
				result = map[string]any{"data": []any{map[string]any{"status": "interrupted"}}}
			case "thread/queue/list":
				result = map[string]any{"data": []any{map[string]any{"id": "oldest-submission"}}}
			case "thread/queue/start":
				if request.Params["queuedSubmissionId"] != "oldest-submission" {
					t.Errorf("started wrong queue item: %v", request.Params)
				}
				result = map[string]any{"turn": map[string]any{"status": "inProgress"}}
			default:
				t.Errorf("unexpected method %q", request.Method)
				return
			}
			response, _ := json.Marshal(map[string]any{"id": request.ID, "result": result})
			_ = conn.Write(r.Context(), websocket.MessageText, response)
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &codexClient{conn: conn}
	defer client.Close()
	if err := codexStartQueuedIfIdle(context.Background(), client, "visible-thread"); err != nil {
		t.Fatal(err)
	}
	methodsMu.Lock()
	defer methodsMu.Unlock()
	if !reflect.DeepEqual(methods, []string{"thread/turns/list", "thread/queue/list", "thread/queue/start"}) {
		t.Fatalf("methods=%v", methods)
	}
}

func TestCodexActiveTurnLeavesNativeQueueAlone(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_, raw, err := conn.Read(r.Context())
		if err != nil {
			t.Error(err)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &request)
		calls.Add(1)
		if request.Method != "thread/turns/list" {
			t.Errorf("unexpected method %q", request.Method)
		}
		response, _ := json.Marshal(map[string]any{"id": request.ID, "result": map[string]any{"data": []any{map[string]any{"status": "inProgress"}}}})
		_ = conn.Write(r.Context(), websocket.MessageText, response)
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &codexClient{conn: conn}
	defer client.Close()
	if err := codexStartQueuedIfIdle(context.Background(), client, "visible-thread"); err != nil || calls.Load() != 1 {
		t.Fatalf("error=%v calls=%d", err, calls.Load())
	}
}

func TestCodexListTurnsUnsupportedUsesThreadReadForSteer(t *testing.T) {
	var methods []string
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
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Error(err)
				return
			}
			methods = append(methods, request.Method)
			response := map[string]any{"id": request.ID, "result": map[string]any{}}
			switch request.Method {
			case "thread/turns/list":
				response["error"] = map[string]any{"message": "list_turns is not supported yet"}
				delete(response, "result")
			case "thread/read":
				response["result"] = map[string]any{"thread": map[string]any{"status": map[string]any{"type": "active", "activeTurnId": "live-turn"}}}
			case "turn/steer":
			default:
				t.Errorf("unexpected method %q", request.Method)
				return
			}
			encoded, _ := json.Marshal(response)
			_ = conn.Write(r.Context(), websocket.MessageText, encoded)
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &codexClient{conn: conn}
	defer client.Close()
	if err := codexSubmitVisibleInput(context.Background(), client, "visible-thread", "message-1", "hello", nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(methods, []string{"thread/turns/list", "thread/read", "turn/steer"}) {
		t.Fatalf("methods=%v", methods)
	}
}

func TestCodexListTurnsUnsupportedStillStartsIdleQueue(t *testing.T) {
	var methods []string
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
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(raw, &request)
			methods = append(methods, request.Method)
			response := map[string]any{"id": request.ID}
			switch request.Method {
			case "thread/turns/list":
				response["error"] = map[string]any{"message": "list_turns is not supported yet"}
			case "thread/read":
				response["result"] = map[string]any{"thread": map[string]any{"status": map[string]any{"type": "idle"}}}
			case "thread/queue/list":
				response["result"] = map[string]any{"data": []any{map[string]any{"id": "queued-1"}}}
			case "thread/queue/start":
				response["result"] = map[string]any{}
			default:
				t.Errorf("unexpected method %q", request.Method)
				return
			}
			encoded, _ := json.Marshal(response)
			_ = conn.Write(r.Context(), websocket.MessageText, encoded)
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &codexClient{conn: conn}
	defer client.Close()
	if err := codexStartQueuedIfIdle(context.Background(), client, "visible-thread"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(methods, []string{"thread/turns/list", "thread/read", "thread/queue/list", "thread/queue/start"}) {
		t.Fatalf("methods=%v", methods)
	}
}

func TestCodexReceiptFindsUserItemBeyondRecentPage(t *testing.T) {
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
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(raw, &request)
			var result map[string]any
			if request.Method == "thread/items/list" {
				result = map[string]any{"data": []any{map[string]any{"item": map[string]any{"type": "agentMessage"}}}}
			} else if request.Method == "thread/read" {
				result = map[string]any{"thread": map[string]any{"turns": []any{map[string]any{"items": []any{map[string]any{"type": "userMessage", "clientId": "old-message"}}}}}}
			} else {
				t.Errorf("unexpected method %q", request.Method)
				return
			}
			response, _ := json.Marshal(map[string]any{"id": request.ID, "result": result})
			_ = conn.Write(r.Context(), websocket.MessageText, response)
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &codexClient{conn: conn}
	defer client.Close()
	accepted, err := codexUserItemPresent(context.Background(), client, "visible-thread", "old-message", true)
	if err != nil || !accepted {
		t.Fatalf("accepted=%t error=%v", accepted, err)
	}
}

func TestCodexNativeRolloutReceiptRequiresExactUserItemAndThread(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "09", "30")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "rollout-2026-09-30T10-00-00-other-thread.jsonl")
	matching := filepath.Join(dir, "rollout-2026-09-30T10-00-01-visible-thread.jsonl")
	if err := os.WriteFile(other, []byte(`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","client_id":"message-1"}}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(matching, []byte(`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","client_id":"message-1"}}}`+"\n"+`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","client_id":"message-2"}}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if found, err := codexUserItemInRollout(root, "visible-thread", "message-1"); err != nil || found {
		t.Fatalf("wrong item/thread receipt: found=%t err=%v", found, err)
	}
	if found, err := codexUserItemInRollout(root, "visible-thread", "message-2"); err != nil || !found {
		t.Fatalf("native receipt: found=%t err=%v", found, err)
	}
}

func TestCodexReceiptFallsBackWhenItemsListingIsUnimplemented(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex", "sessions", "2026", "09", "30")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-09-30T10-00-00-visible-thread.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","client_id":"message-1"}}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_, raw, err := conn.Read(r.Context())
		if err != nil {
			t.Error(err)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &request)
		if request.Method != "thread/items/list" {
			t.Errorf("method=%q", request.Method)
		}
		response, _ := json.Marshal(map[string]any{"id": request.ID, "error": map[string]any{"message": "thread/items/list is not supported yet"}})
		_ = conn.Write(r.Context(), websocket.MessageText, response)
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &codexClient{conn: conn}
	defer client.Close()
	found, err := codexUserItemPresent(context.Background(), client, "visible-thread", "message-1", false)
	if err != nil || !found {
		t.Fatalf("found=%t err=%v", found, err)
	}
}

func TestCodexImageURLTurnInputSupportsLegacyAppServer(t *testing.T) {
	encoded := "R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs="
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pixel.gif")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := codexImageURLTurnInput("inspect this", []string{path})
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"type": "text", "text": "inspect this"},
		{"type": "image", "url": "data:image/gif;base64," + encoded},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("turn input = %#v, want %#v", got, want)
	}
}

func TestCodexImageURLFallbackMatchesOnlyLegacySchemaErrors(t *testing.T) {
	for _, message := range []string{"Invalid request: missing field `url`", "unknown variant `localImage`"} {
		if !codexNeedsImageURLFallback(errors.New(message)) {
			t.Fatalf("legacy schema error %q did not trigger fallback", message)
		}
	}
	if codexNeedsImageURLFallback(errors.New("turn is already running")) {
		t.Fatal("unrelated app-server error triggered image fallback")
	}
}

func TestCodexStartTurnRetriesLegacyImageSchemaOnce(t *testing.T) {
	encoded := "R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs="
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pixel.gif")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var calls [][]map[string]any
	err = codexStartTurnWithFallback("inspect this", []string{path}, func(input []map[string]any) error {
		calls = append(calls, input)
		if len(calls) == 1 {
			return errors.New("codex app server: Invalid request: missing field `url`")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("turn/start calls = %d, want 2", len(calls))
	}
	if calls[0][1]["type"] != "localImage" || calls[1][1]["type"] != "image" {
		t.Fatalf("turn/start inputs = %#v", calls)
	}
}

func TestCodexStartTurnDoesNotRetryUnrelatedError(t *testing.T) {
	calls := 0
	want := errors.New("turn is already running")
	err := codexStartTurnWithFallback("inspect this", []string{"/tmp/image.png"}, func([]map[string]any) error {
		calls++
		return want
	})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("error = %v, calls = %d; want original error and one call", err, calls)
	}
}

func TestCodexImageURLTurnInputRejectsUnreadableImage(t *testing.T) {
	_, err := codexImageURLTurnInput("inspect this", []string{filepath.Join(t.TempDir(), "missing.png")})
	if err == nil || !strings.Contains(err.Error(), "read Codex image fallback") {
		t.Fatalf("error = %v, want image read failure", err)
	}
}
