package boxruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type lineCapture struct {
	mu    sync.Mutex
	data  []byte
	lines chan []byte
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
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "claude-order")
	imageData := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	if err := StoreChatInbound(home, "claude-order", ChatInbound{ID: "message-1", Text: "hello", Images: []ChatEventImage{{Name: "screen.png", MediaType: "image/png", Data: imageData}}}); err != nil {
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
	var response desktopMCPRequest
	if json.Unmarshal(first, &response) != nil || string(response.ID) != "1" || response.Method != "" {
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
		if params.Content != "hello" || params.Meta["image_path"] == "" || params.Meta["file_path"] != "" {
			t.Fatalf("Claude image attachment was not advertised as image_path: %+v", params)
		}
		if _, err := os.Stat(params.Meta["image_path"]); err != nil {
			t.Fatalf("channel image path is not readable: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel notification was not emitted after initialize")
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
	if err := ServeDesktopMCP(context.Background(), "assignment", strings.NewReader(string(request)+"\n"), &output); err != nil {
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

func TestClaudeChannelOldProcessCannotRemoveReplacementReadiness(t *testing.T) {
	home := t.TempDir()
	dir := claudeChannelReadyDir(home)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	oldPath := claudeChannelReadyPath(home, "session", "channel_old")
	newPath := claudeChannelReadyPath(home, "session", "channel_new")
	for _, path := range []string{oldPath, newPath} {
		if err := os.WriteFile(path, []byte(filepath.Base(path)), 0600); err != nil {
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
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"desktop_click","arguments":{"x":5}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"desktop_screenshot","arguments":{"action":"shell"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"shell","arguments":{}}}`,
	}, "\n")
	var out bytes.Buffer
	if err := ServeDesktopMCP(context.Background(), "invalid", strings.NewReader(input), &out); err != nil {
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
	for _, fragment := range []string{"# vmbox-desktop MCP tools", "## chat_message", "## type_secret", "## desktop_screenshot", `Schema: `} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("guide missing %q: %s", fragment, text)
		}
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
