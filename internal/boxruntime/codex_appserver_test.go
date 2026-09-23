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
	"testing"

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

func TestCodexCurrentThreadPrefersFirstTurnAfterClear(t *testing.T) {
	root := t.TempDir()
	if err := writeTextAtomic(codexResetPendingFile(root, "session"), "fresh-thread\n", 0600); err != nil {
		t.Fatal(err)
	}
	// No app-server query is needed while the freshly created thread is idle;
	// the older conversation may still appear more recently active in its list.
	id, err := CodexCurrentThread(context.Background(), nil, root, "session", "/workspace")
	if err != nil || id != "fresh-thread" {
		t.Fatalf("thread=%q err=%v", id, err)
	}
}

func TestNewestCreatedCodexThreadAfterVisibleClear(t *testing.T) {
	threads := []any{
		map[string]any{"id": "old-busy", "createdAt": float64(10), "recencyAt": float64(100)},
		map[string]any{"id": "new-visible", "createdAt": float64(20), "recencyAt": float64(20)},
	}
	if got := newestCreatedCodexThread(threads); got != "new-visible" {
		t.Fatalf("visible reset picked %q", got)
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
