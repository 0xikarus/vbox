package boxruntime

import (
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
	if err := StoreChatInbound(home, "claude-order", ChatInbound{ID: "message-1", Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input, write := io.Pipe()
	defer write.Close()
	output := &lineCapture{lines: make(chan []byte, 4)}
	done := make(chan error, 1)
	go func() { done <- ServeDesktopMCP(ctx, "invalid", input, output) }()

	ready := filepath.Join(home, ".local", "share", "vmbox", "chat", "channel-ready", "claude-order")
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(ready); !os.IsNotExist(err) {
		t.Fatalf("channel advertised readiness before initialize: %v", err)
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
	if _, err := os.Stat(ready); !os.IsNotExist(err) {
		t.Fatalf("channel advertised readiness before initialized notification: %v", err)
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
		if i == 1 && len(response.Result["tools"].([]any)) != 13 {
			t.Fatal("tool inventory incomplete")
		}
		if i >= 2 && response.Result["isError"] != true {
			t.Fatal("invalid call accepted")
		}
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
