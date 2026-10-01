package boxruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

type lineCapture struct {
	mu    sync.Mutex
	data  []byte
	lines chan []byte
}

func TestCreateAgentBoxToolDescribesStartupInstructions(t *testing.T) {
	for _, tool := range desktopMCPTools() {
		if tool["name"] != "create_agent_box" {
			continue
		}
		schema, ok := tool["inputSchema"].(map[string]any)
		if !ok {
			t.Fatalf("input schema=%T", tool["inputSchema"])
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("properties=%T", schema["properties"])
		}
		instructions, ok := properties["instructions"].(map[string]any)
		if !ok || instructions["type"] != "string" || instructions["maxLength"] != v1.MaxInstructionMarkdownBytes {
			t.Fatalf("instructions schema=%#v", instructions)
		}
		presets, ok := properties["tools"].(map[string]any)
		if !ok || presets["type"] != "array" || presets["maxItems"] != 3 {
			t.Fatalf("tool preset schema=%#v", properties["tools"])
		}
		if properties["loginProfiles"] == nil || properties["roleIds"] == nil || properties["slotId"] == nil {
			t.Fatalf("creation config missing from schema: %#v", properties)
		}
		for name, bounds := range map[string][2]int{"memoryGiB": {1, 8}, "swapGiB": {0, 4}} {
			limit, ok := properties[name].(map[string]any)
			if !ok || limit["type"] != "integer" || limit["minimum"] != bounds[0] || limit["maximum"] != bounds[1] {
				t.Fatalf("%s schema=%#v", name, properties[name])
			}
		}
		return
	}
	t.Fatal("create_agent_box tool is missing")
}

func TestAgentBoxLifecycleToolRequiresTargetConfirmationAndRetryKey(t *testing.T) {
	for _, name := range []string{"wake_agent_box", "clear_agent_box_context", "compact_agent_box_context"} {
		found := false
		for _, tool := range desktopMCPTools() {
			if tool["name"] != name {
				continue
			}
			found = true
			schema := tool["inputSchema"].(map[string]any)
			required := schema["required"].([]string)
			if !slices.Equal(required, []string{"box", "confirmation", "idempotencyKey"}) {
				t.Fatalf("%s required fields=%v", name, required)
			}
		}
		if !found {
			t.Fatalf("%s tool is missing", name)
		}
	}
}

func TestWakeAgentBoxToolOffersSessionChoice(t *testing.T) {
	for _, tool := range desktopMCPTools() {
		if tool["name"] != "wake_agent_box" {
			continue
		}
		choice := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)["sessionChoice"].(map[string]any)
		if !reflect.DeepEqual(choice["enum"], []string{"restore", "fresh"}) {
			t.Fatalf("sessionChoice schema=%v", choice)
		}
		return
	}
	t.Fatal("wake_agent_box tool is missing")
}

func TestContactSendReturnsControllerRejectionAndRemovesAcknowledgedEvent(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent-desktop/chat-ready" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var payload struct {
			Event ChatEvent `json:"event"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Event.Contact != "mascot" {
			t.Errorf("unexpected event %+v: %v", payload.Event, err)
		}
		_, _ = w.Write([]byte(`{"stored":true,"delivered":false,"reason":"mascot is hibernated"}`))
	}))
	defer server.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "codex-test")
	certFile := filepath.Join(home, "test-ca.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", certFile)
	configPath := filepath.Join(home, ".config", "vmbox", "desktop-agent.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(DesktopAgentConfig{Controller: server.URL, Assignment: "assignment", Token: strings.Repeat("a", 64)})
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := sendDesktopContactEvent(context.Background(), "assignment", ChatEvent{Kind: "contact", Contact: "mascot", Text: "wake up"})
	if err == nil || !strings.Contains(err.Error(), "mascot is hibernated") {
		t.Fatalf("rejection not returned to sender: %v", err)
	}
	if event, found, err := PullChatEvent(home, "codex-test"); err != nil || found {
		t.Fatalf("acknowledged rejection was left in outbox: event=%+v found=%t err=%v", event, found, err)
	}
}

func TestContactSendKeepsQueuedEventWhenControllerDefersDelivery(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"contact delivery deferred"}`))
	}))
	defer server.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "codex-test")
	certFile := filepath.Join(home, "test-ca.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", certFile)
	configPath := filepath.Join(home, ".config", "vmbox", "desktop-agent.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(DesktopAgentConfig{Controller: server.URL, Assignment: "assignment", Token: strings.Repeat("a", 64)})
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	confirmation, err := sendDesktopContactEvent(context.Background(), "assignment", ChatEvent{Kind: "contact", Contact: "mascot", Text: "hello"})
	if err != nil || !strings.Contains(confirmation, "retry automatically") {
		t.Fatalf("confirmation=%q err=%v", confirmation, err)
	}
	if event, found, err := PullChatEvent(home, "codex-test"); err != nil || !found || event.Text != "hello" {
		t.Fatalf("deferred event was not retained: event=%+v found=%t err=%v", event, found, err)
	}
}

func TestAgentBoxScreenshotToolReturnsAnImageSchema(t *testing.T) {
	for _, tool := range desktopMCPTools() {
		if tool["name"] != "get_agent_box_screenshot" {
			continue
		}
		schema := tool["inputSchema"].(map[string]any)
		if !slices.Equal(schema["required"].([]string), []string{"box"}) {
			t.Fatalf("required screenshot fields=%v", schema["required"])
		}
		properties := schema["properties"].(map[string]any)
		if properties["thumbnail"].(map[string]any)["type"] != "boolean" {
			t.Fatalf("thumbnail field=%v", properties["thumbnail"])
		}
		if _, err := callDesktopTool(context.Background(), "assignment", "get_agent_box_screenshot", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "missing required argument") {
			t.Fatalf("missing box error=%v", err)
		}
		return
	}
	t.Fatal("get_agent_box_screenshot tool is missing")
}

func TestAgentBoxScreenshotToolReturnsControllerPNG(t *testing.T) {
	var imageBytes bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&imageBytes, img); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent-desktop/boxes/target/screenshot" || r.URL.Query().Get("thumbnail") != "true" || r.Header.Get("Authorization") != "DesktopAgent "+strings.Repeat("a", 64) {
			t.Errorf("unexpected screenshot request: %s", r.URL.String())
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(imageBytes.Bytes())
	}))
	defer server.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	certFile := filepath.Join(home, "test-ca.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", certFile)
	configPath := filepath.Join(home, ".config", "vmbox", "desktop-agent.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(DesktopAgentConfig{Controller: server.URL, Assignment: "assignment", Token: strings.Repeat("a", 64)})
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := callDesktopTool(context.Background(), "assignment", "get_agent_box_screenshot", json.RawMessage(`{"box":"target","thumbnail":true}`))
	if err != nil {
		t.Fatal(err)
	}
	content := result["content"].([]map[string]any)
	if len(content) != 1 || content[0]["type"] != "image" || content[0]["mimeType"] != "image/png" {
		t.Fatalf("screenshot content=%v", content)
	}
	decoded, err := base64.StdEncoding.DecodeString(content[0]["data"].(string))
	if err != nil || !bytes.Equal(decoded, imageBytes.Bytes()) {
		t.Fatalf("screenshot image invalid: %v", err)
	}
}

func TestAgentBoxConfigToolUsesExplicitMode(t *testing.T) {
	for _, test := range []struct {
		args string
		want string
	}{
		{`{"mode":"list"}`, "/v1/agent-desktop/box-configs"},
		{`{"mode":"models","application":"opencode","name":"venice"}`, "/v1/agent-desktop/box-configs/opencode/venice/models"},
		{`{"mode":"models","application":"claude","name":"personal"}`, "/v1/agent-desktop/box-configs/claude/personal/models"},
		{`{}`, ""},
		{`{"mode":"models","application":"opencode"}`, ""},
		{`{"mode":"list","application":"opencode","name":"venice"}`, ""},
	} {
		got, err := agentBoxConfigPath(json.RawMessage(test.args))
		if got != test.want || (err != nil) != (test.want == "") {
			t.Fatalf("args=%s path=%q error=%v", test.args, got, err)
		}
	}
}

func TestRetiredCoordinationToolsAreNotCallable(t *testing.T) {
	for _, tool := range desktopMCPTools() {
		if v1.RetiredCoordinationMCPTools[tool["name"].(string)] {
			t.Fatalf("retired tool %s is still advertised", tool["name"])
		}
	}
	tools, allowed, err := allowedDesktopMCPTools(context.Background(), "assignment", func(context.Context, string) (map[string]bool, error) {
		return map[string]bool{"chat_message": true, "request_more_time": true, "queue_followup": true, "create_email_address": true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0]["name"] != "chat_message" || !allowed["chat_message"] {
		t.Fatalf("unexpected callable tools: %v", tools)
	}
	for name := range v1.RetiredCoordinationMCPTools {
		if allowed[name] {
			t.Fatalf("retired tool %s remains callable", name)
		}
	}
}

func TestChatReadyPushAcknowledgesOnlyConfirmedText(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "codex-test")
	event := ChatEvent{ID: "0123456789ab", Kind: "reply", Text: "done"}
	if err := writeChatEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	var calls int
	notifyDesktopChatReady(context.Background(), "assignment", home, "codex-test", event, func(_ context.Context, _, method, path string, input, output any) error {
		calls++
		if method != http.MethodPost || path != "/v1/agent-desktop/chat-ready" || calls != 1 {
			t.Fatalf("unexpected callback %d %s %s", calls, method, path)
		}
		request := input.(map[string]any)
		if request["session"] != "codex-test" || !reflect.DeepEqual(request["event"], event) {
			t.Fatalf("wrong scoped event: %v", request)
		}
		return json.Unmarshal([]byte(`{"stored":true}`), output)
	})
	if calls != 1 {
		t.Fatalf("callbacks=%d", calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".local/share/vmbox/chat/outbox/codex-test", event.ID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("confirmed event remains in outbox: %v", err)
	}
}

func TestChatReadyPushFallsBackWithoutDiscardingOutbox(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "codex-test")
	event := ChatEvent{ID: "0123456789ab", Kind: "contact", Contact: "peer", Text: "hello"}
	if err := writeChatEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	var calls int
	notifyDesktopChatReady(context.Background(), "assignment", home, "codex-test", event, func(_ context.Context, _, _, _ string, input, _ any) error {
		calls++
		if calls == 1 {
			return errors.New("controller temporarily unavailable")
		}
		if _, ok := input.(map[string]string); !ok {
			t.Fatalf("fallback did not send a legacy hint: %T", input)
		}
		return nil
	})
	if calls != 2 {
		t.Fatalf("callbacks=%d", calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".local/share/vmbox/chat/outbox/codex-test", event.ID+".json")); err != nil {
		t.Fatalf("unconfirmed event lost from outbox: %v", err)
	}
}

func TestChatReadyPushRetriesHintAfterDirectTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	notifyDesktopChatReady(ctx, "assignment", t.TempDir(), "codex-test", ChatEvent{ID: "0123456789ab", Kind: "reply", Text: "done"}, func(requestCtx context.Context, _, _, _ string, input, _ any) error {
		calls++
		if calls == 1 {
			cancel()
			return context.DeadlineExceeded
		}
		if requestCtx.Err() != nil {
			t.Fatalf("fallback inherited the failed direct callback context: %v", requestCtx.Err())
		}
		if _, ok := input.(map[string]string); !ok {
			t.Fatalf("fallback did not send a legacy hint: %T", input)
		}
		return nil
	})
	if calls != 2 {
		t.Fatalf("callbacks=%d", calls)
	}
}

func TestChatReadyImageUsesOutboxPull(t *testing.T) {
	event := ChatEvent{ID: "0123456789ab", Kind: "contact", Text: "see image", Images: []ChatEventImage{{MediaType: "image/png", Data: "aGVsbG8="}}}
	var calls int
	notifyDesktopChatReady(context.Background(), "assignment", t.TempDir(), "codex-test", event, func(_ context.Context, _, _, _ string, input, _ any) error {
		calls++
		if _, ok := input.(map[string]string); !ok {
			t.Fatalf("image payload was pushed instead of a drain hint: %T", input)
		}
		return nil
	})
	if calls != 1 {
		t.Fatalf("callbacks=%d", calls)
	}
}

func (c *lineCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = append(c.data, p...)
	for {
		index := bytes.IndexByte(c.data, '\n')
		if index < 0 {
			break
		}
		line := append([]byte(nil), c.data[:index]...)
		c.data = c.data[index+1:]
		c.lines <- line
	}
	return len(p), nil
}

func TestDesktopMCPStartsChannelAfterInitializeResponse(t *testing.T) {
	home := t.TempDir()
	previousCurrent := claudeChannelCurrent
	claudeChannelCurrent = func(context.Context, string) bool { return true }
	t.Cleanup(func() { claudeChannelCurrent = previousCurrent })
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "claude-order")
	imageData := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	if err := StoreChatInbound(home, "claude-order", ChatInbound{ID: "message-1", Text: "hello", Images: []ChatEventImage{{Name: "screen.png", MediaType: "image/png", Data: imageData}}}); err != nil {
		t.Fatal(err)
	}
	if err := StoreChatInbound(home, "claude-order", ChatInbound{ID: "message-2", Text: "follow up", Images: []ChatEventImage{{Name: "screen-2.png", MediaType: "image/png", Data: imageData}}}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input, write := io.Pipe()
	defer write.Close()
	output := &lineCapture{lines: make(chan []byte, 4)}
	done := make(chan error, 1)
	go func() { done <- ServeDesktopMCP(ctx, "invalid", input, output) }()

	ready := func() bool {
		owners, err := claudeChannelOwners(home, "claude-order")
		return err == nil && len(owners) > 0
	}
	time.Sleep(50 * time.Millisecond)
	if ready() {
		t.Fatal("channel advertised readiness before initialize")
	}
	select {
	case line := <-output.lines:
		t.Fatalf("MCP emitted before initialize: %s", line)
	default:
	}

	if _, err := io.WriteString(write, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"claude-code","version":"2.1.259"}}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	first := <-output.lines
	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  struct {
			Capabilities struct {
				Tools struct {
					ListChanged bool `json:"listChanged"`
				} `json:"tools"`
			} `json:"capabilities"`
		} `json:"result"`
	}
	if json.Unmarshal(first, &response) != nil || string(response.ID) != "1" || !response.Result.Capabilities.Tools.ListChanged {
		t.Fatalf("first output was not initialize response: %s", first)
	}
	time.Sleep(50 * time.Millisecond)
	if ready() {
		t.Fatal("channel advertised readiness before initialized notification")
	}
	select {
	case line := <-output.lines:
		t.Fatalf("channel emitted before initialized notification: %s", line)
	default:
	}
	if _, err := io.WriteString(write, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	for index, wantText := range []string{"hello", "follow up"} {
		select {
		case second := <-output.lines:
			var notification desktopMCPRequest
			if json.Unmarshal(second, &notification) != nil || notification.Method != "notifications/claude/channel" {
				t.Fatalf("second output was not channel notification: %s", second)
			}
			var params struct {
				Content string            `json:"content"`
				Meta    map[string]string `json:"meta"`
			}
			if err := json.Unmarshal(notification.Params, &params); err != nil {
				t.Fatal(err)
			}
			if params.Content != wantText || params.Meta["image_path"] == "" || params.Meta["file_path"] != "" {
				t.Fatalf("Claude image attachment was not advertised as image_path: %+v", params)
			}
			if _, err := os.Stat(params.Meta["image_path"]); err != nil {
				t.Fatalf("channel image path is not readable: %v", err)
			}
			inbox := filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", "claude-order", params.Meta["message_id"]+".json")
			if _, err := os.Stat(inbox); err != nil {
				t.Fatalf("channel transport write removed durable inbox before native receipt: %v", err)
			}
			// The channel may send the next pending event before the first
			// native receipt. It must keep both inbox files until each exact
			// user record appears in Claude's transcript.
			transcript := filepath.Join(home, ".claude", "projects", "test", "conversation.jsonl")
			if err := os.MkdirAll(filepath.Dir(transcript), 0700); err != nil {
				t.Fatal(err)
			}
			record, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "<channel source=\"vmbox-desktop\" chat_id=\"claude-order\" message_id=\"" + params.Meta["message_id"] + "\">\n" + wantText + "\n</channel>"}})
			flag := os.O_CREATE | os.O_WRONLY
			if index > 0 {
				flag |= os.O_APPEND
			}
			file, err := os.OpenFile(transcript, flag, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = file.Write(append(record, '\n'))
			if closeErr := file.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("channel notification for %q was not emitted after initialize", wantText)
		}
	}

	cancel()
	_ = write.Close()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("MCP server did not stop")
	}
}

func TestClaudeChannelDoesNotResendWhileBusyOnSameConnection(t *testing.T) {
	event := chatInboundFile{ID: "message-1", Text: "queued during a long turn"}
	sent := map[string]bool{}
	calls := 0
	encode := func(value any) error {
		calls++
		notification := value.(map[string]any)
		if notification["method"] != "notifications/claude/channel" {
			t.Fatalf("unexpected notification: %v", notification)
		}
		params := notification["params"].(map[string]any)
		meta := params["meta"].(map[string]string)
		if params["content"] != event.Text || meta["message_id"] != event.ID {
			t.Fatalf("wrong channel payload: %v", params)
		}
		return nil
	}
	if !emitClaudeChannelEvent("claude-session", event, sent, encode) {
		t.Fatal("first channel delivery was not emitted")
	}
	if emitClaudeChannelEvent("claude-session", event, sent, encode) || calls != 1 {
		t.Fatalf("queued message was resent on the same connection: %d calls", calls)
	}
	// A replacement Claude MCP process gets a new connection after the old TUI
	// exits. An inbox without a native receipt must be delivered there.
	if !emitClaudeChannelEvent("claude-session", event, map[string]bool{}, encode) || calls != 2 {
		t.Fatalf("replacement connection did not retry the inbox: %d calls", calls)
	}
}

func TestClaudeChannelRecoversAfterTemporaryTmuxOutage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "claude-recover")
	if err := StoreChatInbound(home, "claude-recover", ChatInbound{ID: "message-1", Text: "after outage"}); err != nil {
		t.Fatal(err)
	}
	previousCurrent, previousInterval := claudeChannelCurrent, claudeChannelPollInterval
	var current atomic.Bool
	var checks atomic.Int32
	claudeChannelCurrent = func(context.Context, string) bool {
		checks.Add(1)
		return current.Load()
	}
	claudeChannelPollInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		claudeChannelCurrent, claudeChannelPollInterval = previousCurrent, previousInterval
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notifications := make(chan map[string]any, 2)
	done := make(chan struct{})
	go func() {
		serveClaudeChannel(ctx, func(value any) error {
			notifications <- value.(map[string]any)
			return nil
		})
		close(done)
	}()
	deadline := time.After(time.Second)
	for checks.Load() < 4 {
		select {
		case <-deadline:
			t.Fatal("channel stopped checking tmux during the outage")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if owners, err := claudeChannelOwners(home, "claude-recover"); err != nil || len(owners) != 0 {
		t.Fatalf("channel advertised readiness during outage: owners=%v error=%v", owners, err)
	}
	current.Store(true)
	select {
	case notification := <-notifications:
		if notification["method"] != "notifications/claude/channel" {
			t.Fatalf("unexpected notification: %v", notification)
		}
		params := notification["params"].(map[string]any)
		if params["content"] != "after outage" {
			t.Fatalf("wrong message after recovery: %v", params)
		}
	case <-time.After(time.Second):
		t.Fatal("pending message was not emitted after tmux recovered")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("channel did not stop after cancellation")
	}
}

func TestDesktopMCPAcceptsMaximumChatMessageFrame(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "codex-long-reply")
	text := strings.Repeat("<", 100_000)
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "chat_message",
			"arguments": map[string]any{"text": text},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(request) <= bufio.MaxScanTokenSize {
		t.Fatalf("test request is only %d bytes; it does not exercise the old scanner limit", len(request))
	}
	var output bytes.Buffer
	allowChat := func(context.Context, string) (map[string]bool, error) {
		return map[string]bool{"chat_message": true}, nil
	}
	if err := serveDesktopMCP(context.Background(), "assignment", strings.NewReader(string(request)+"\n"), &output, allowChat); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("invalid MCP response %q: %v", output.String(), err)
	}
	if _, failed := response["error"]; failed {
		t.Fatalf("maximum-size chat reply was rejected: %v", response)
	}
	event, found, err := PullChatEvent(home, "codex-long-reply")
	if err != nil || !found {
		t.Fatalf("long chat event unavailable: found=%t err=%v", found, err)
	}
	if event.Text != text {
		t.Fatalf("stored reply has %d bytes, want %d", len(event.Text), len(text))
	}
}

func TestWatchDesktopToolPolicyNotifiesRunningClient(t *testing.T) {
	previousInterval := desktopToolPolicyPollInterval
	desktopToolPolicyPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { desktopToolPolicyPollInterval = previousInterval })
	var calls atomic.Int32
	resolve := func(context.Context, string) (map[string]bool, error) {
		if calls.Add(1) == 1 {
			return map[string]bool{"chat_message": true}, nil
		}
		return map[string]bool{"chat_message": true, "take_screenshot": true}, nil
	}
	notifications := make(chan map[string]any, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchDesktopToolPolicy(ctx, "assignment", resolve, func(value any) error {
		notifications <- value.(map[string]any)
		return nil
	})
	select {
	case notification := <-notifications:
		if notification["method"] != "notifications/tools/list_changed" {
			t.Fatalf("notification=%v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("tool-list change was not advertised")
	}
}

func TestClaudeChannelOldProcessCannotRemoveReplacementReadiness(t *testing.T) {
	home := t.TempDir()
	dir := claudeChannelReadyDir(home)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	oldPath := claudeChannelReadyPath(home, "session", "channel_old")
	newPath := claudeChannelReadyPath(home, "session", "channel_new")
	for _, path := range []string{oldPath, newPath} {
		marker, _ := json.Marshal(map[string]any{"owner": strings.TrimPrefix(filepath.Base(path), "session."), "pid": os.Getpid()})
		if err := os.WriteFile(path, marker, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(oldPath); err != nil {
		t.Fatal(err)
	}
	owners, err := claudeChannelOwners(home, "session")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := owners["channel_new"]; !ok {
		t.Fatalf("old process removal affected replacement readiness: %v", owners)
	}
}

func TestDesktopMCPDoesNotConsumeClaudeChannelInboxForOtherClients(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "opencode-session")
	if err := StoreChatInbound(home, "opencode-session", ChatInbound{ID: "message-1", Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	input, write := io.Pipe()
	output := &lineCapture{lines: make(chan []byte, 4)}
	done := make(chan error, 1)
	go func() { done <- ServeDesktopMCP(ctx, "invalid", input, output) }()
	if _, err := io.WriteString(write, strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"opencode","version":"1.18.27"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	}, "\n")+"\n"); err != nil {
		t.Fatal(err)
	}
	<-output.lines
	time.Sleep(100 * time.Millisecond)
	ready := filepath.Join(home, ".local", "share", "vmbox", "chat", "channel-ready", "opencode-session")
	if _, err := os.Stat(ready); !os.IsNotExist(err) {
		t.Fatalf("non-Claude client advertised channel readiness: %v", err)
	}
	inbox := filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", "opencode-session", "message-1.json")
	if _, err := os.Stat(inbox); err != nil {
		t.Fatalf("non-Claude client consumed Agent chat inbox: %v", err)
	}
	select {
	case line := <-output.lines:
		t.Fatalf("non-Claude client received channel notification: %s", line)
	default:
	}
	cancel()
	_ = write.Close()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("MCP server did not stop")
	}
}

func TestDesktopMCPNegotiationAndInvalidCalls(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"click_mouse","arguments":{"x":5}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"take_screenshot","arguments":{"action":"shell"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"shell","arguments":{}}}`,
	}, "\n")
	var out bytes.Buffer
	if err := serveDesktopMCP(context.Background(), "invalid", strings.NewReader(input), &out, allDesktopToolPolicy); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("unexpected responses: %s", out.String())
	}
	for i, line := range lines {
		var response struct {
			ID     int            `json:"id"`
			Result map[string]any `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		if response.ID != i+1 {
			t.Fatal("response identity lost")
		}
		if i == 0 && response.Result["protocolVersion"] != "2025-11-25" {
			t.Fatal("version negotiation failed")
		}
		if i == 1 && len(response.Result["tools"].([]any)) != len(desktopMCPTools()) {
			t.Fatal("tool inventory incomplete")
		}
		if i >= 2 && response.Result["isError"] != true {
			t.Fatal("invalid call accepted")
		}
	}
}

func TestAllowedDesktopMCPToolsFiltersAdvertisedInventory(t *testing.T) {
	resolve := func(context.Context, string) (map[string]bool, error) {
		return map[string]bool{"chat_message": true, "take_screenshot": true}, nil
	}
	tools, allowed, err := allowedDesktopMCPTools(context.Background(), "assignment", resolve)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || !allowed["chat_message"] || !allowed["take_screenshot"] {
		t.Fatalf("filtered tools=%v allowed=%v", tools, allowed)
	}
	for _, tool := range tools {
		if name := tool["name"].(string); name != "chat_message" && name != "take_screenshot" {
			t.Fatalf("unexpected tool %s", name)
		}
	}
}

func TestDesktopMCPInventoryMatchesRolePolicyNames(t *testing.T) {
	want := append(append([]string{}, v1.BasicAgentMCPTools...), v1.OptionalAgentMCPTools...)
	got := make([]string, 0, len(desktopMCPTools()))
	for _, tool := range desktopMCPTools() {
		got = append(got, tool["name"].(string))
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("desktop MCP names=%v; role policy names=%v", got, want)
	}
}

func TestDesktopMCPGuideMatchesAdvertisedTools(t *testing.T) {
	home := t.TempDir()
	if err := writeDesktopMCPGuide(home); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, desktopMCPGuidePath)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, fragment := range []string{"# vmbox-desktop MCP tools", "## Contacting other boxes", "get_contacts {}", `chat_message {"contact":"reviewer"`, `chat_message {"contact":"a1b2c3d4"`, `chat_message {"contact":"a1b2c3d4-1234-4000-8000-000000000000"`, "exact box `name`", "## Send a prompt from a box-local app", "mcp-http.json", "promptUrl", `{"text":"Check the latest build result"}`, "Authorization: Bearer", "## chat_message", "## type_secret", "## take_screenshot", `Schema: `} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("guide missing %q: %s", fragment, text)
		}
	}
	if strings.Contains(text, "chat_message(text=") {
		t.Fatalf("guide contains legacy pseudo-function syntax: %s", text)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("guide mode = %v", info.Mode().Perm())
	}
}

func TestSaveDesktopScreenshotUsesPrivateWorkspacePath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("VMBOX_WORKSPACE_ROOT", root)
	want := []byte("png bytes")
	path, err := saveDesktopScreenshot(want)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(root, "tmp", "vmbox", "desktop-screenshot.png") {
		t.Fatalf("unexpected screenshot path %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("saved screenshot = %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("screenshot mode = %v", info.Mode().Perm())
	}
}

func TestDesktopDoubleClickHasInterClickDelay(t *testing.T) {
	var events []string
	var clicks []time.Time
	err := desktopClicks(context.Background(), 2, 1, func() error {
		events = append(events, "check")
		return nil
	}, func(args ...string) error {
		events = append(events, strings.Join(args, " "))
		clicks = append(clicks, time.Now())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"check", "click 1", "check", "click 1"}) {
		t.Fatalf("events=%v", events)
	}
	if gap := clicks[1].Sub(clicks[0]); gap < desktopDoubleClickDelay {
		t.Fatalf("inter-click gap=%s, want at least %s", gap, desktopDoubleClickDelay)
	}
}

func TestDesktopDoubleClickCanBeInterruptedBetweenClicks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clicks := 0
	err := desktopClicks(ctx, 2, 1, func() error { return nil }, func(...string) error {
		clicks++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || clicks != 1 {
		t.Fatalf("err=%v clicks=%d", err, clicks)
	}
}

func TestDesktopInputValidation(t *testing.T) {
	for _, data := range []string{
		`{"action":"key","keys":["exec"]}`,
		`{"action":"move","x":1280,"y":0}`,
		`{"action":"drag","x":0,"y":0,"toX":-1}`,
		`{"action":"type","text":""}`,
		`{"action":"scroll","text":"sideways"}`,
		`{"action":"pause","command":"rm"}`,
	} {
		if _, err := DecodeDesktopAction([]byte(data)); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	if _, err := DecodeDesktopAction([]byte(`{"action":"type","text":"$(not a shell)"}`)); err != nil {
		t.Fatal(err)
	}
}
