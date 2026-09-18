package boxruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// CodexChatPort maps a tmux session to the loopback port its app server listens
// on, the way OpenCodeChatPort does. Codex drives the agent through that server
// and the terminal attaches to it with --remote, so the box shows exactly the
// thread chat is talking to.
func CodexChatPort(session string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte("codex:" + session))
	return 30_000 + int(hash.Sum32()%10_000)
}

func codexAppServerURL(session string) string {
	return fmt.Sprintf("ws://127.0.0.1:%d", CodexChatPort(session))
}

// codexThreadFile remembers the thread the terminal is on. The id changes
// whenever the user starts a new conversation, so it is refreshed from the
// server's thread/started notifications rather than pinned at launch.
func codexThreadFile(root, session string) string {
	return filepath.Join(root, "chat", "codex-threads", session)
}

// codexClient is one connection to a session's app server.
type codexClient struct {
	conn   *websocket.Conn
	nextID atomic.Int64
}

func dialCodexAppServer(ctx context.Context, session string) (*codexClient, error) {
	conn, _, err := websocket.Dial(ctx, codexAppServerURL(session), &websocket.DialOptions{HTTPClient: &http.Client{Timeout: 20 * time.Second}})
	if err != nil {
		return nil, fmt.Errorf("codex app server unavailable: %w", err)
	}
	conn.SetReadLimit(32 << 20)
	client := &codexClient{conn: conn}
	if err := client.handshake(ctx); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

func (c *codexClient) Close() { _ = c.conn.Close(websocket.StatusNormalClosure, "") }

func (c *codexClient) send(ctx context.Context, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.conn.Write(ctx, websocket.MessageText, data)
}

func (c *codexClient) read(ctx context.Context) (map[string]any, error) {
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var message map[string]any
	if err := json.Unmarshal(data, &message); err != nil {
		return nil, fmt.Errorf("invalid app server message")
	}
	return message, nil
}

// handshake performs the initialize exchange the app server requires before it
// will answer anything else.
func (c *codexClient) handshake(ctx context.Context) error {
	id := c.nextID.Add(1)
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": "initialize", "params": map[string]any{
		"clientInfo":   map[string]any{"name": "vmbox", "version": "1.0.0"},
		"capabilities": map[string]any{"experimentalApi": true, "optOutNotificationMethods": []string{}},
	}}
	if err := c.send(ctx, request); err != nil {
		return err
	}
	if _, err := c.await(ctx, id); err != nil {
		return err
	}
	return c.send(ctx, map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}})
}

// await reads until the reply to id arrives, recording any thread the server
// reports along the way so a conversation started in the terminal is noticed.
func (c *codexClient) await(ctx context.Context, id int64) (map[string]any, error) {
	for {
		message, err := c.read(ctx)
		if err != nil {
			return nil, err
		}
		if raw, ok := message["id"]; ok {
			if number, ok := raw.(float64); ok && int64(number) == id {
				if failure, bad := message["error"]; bad {
					return nil, fmt.Errorf("codex app server: %s", codexErrorText(failure))
				}
				result, _ := message["result"].(map[string]any)
				return result, nil
			}
		}
	}
}

func codexErrorText(value any) string {
	failure, _ := value.(map[string]any)
	if text, ok := failure["message"].(string); ok {
		return text
	}
	return "request rejected"
}

// call issues one request and returns its result.
func (c *codexClient) call(ctx context.Context, method string, params any) (map[string]any, error) {
	id := c.nextID.Add(1)
	if err := c.send(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	return c.await(ctx, id)
}

// CodexCurrentThread reports the most recently active thread for the workspace.
// A remembered id is only a fallback for old servers that omit recencyAt. It
// must not pin delivery to an earlier thread after the user starts or resumes a
// different conversation in the terminal.
func CodexCurrentThread(ctx context.Context, client *codexClient, root, session, workspace string) (string, error) {
	remembered := ""
	if data, err := os.ReadFile(codexThreadFile(root, session)); err == nil {
		remembered = strings.TrimSpace(string(data))
	}
	result, err := client.call(ctx, "thread/list", map[string]any{})
	if err != nil {
		return "", err
	}
	threads, _ := result["data"].([]any)
	newest := newestCodexThread(threads, remembered)
	if newest != "" {
		return newest, rememberCodexThread(root, session, newest)
	}
	// Nothing recorded yet: start the thread ourselves so the terminal has one
	// to attach to.
	started, err := client.call(ctx, "thread/start", map[string]any{"cwd": workspace})
	if err != nil {
		return "", err
	}
	thread, _ := started["thread"].(map[string]any)
	id, _ := thread["id"].(string)
	if id == "" {
		return "", fmt.Errorf("codex app server returned no thread")
	}
	return id, rememberCodexThread(root, session, id)
}

func newestCodexThread(threads []any, remembered string) string {
	newest, fallback := "", ""
	newestAt := float64(0)
	hasRecency, rememberedFound := false, false
	for _, value := range threads {
		thread, _ := value.(map[string]any)
		id, _ := thread["id"].(string)
		if id == "" {
			continue
		}
		if fallback == "" {
			fallback = id
		}
		if id == remembered {
			rememberedFound = true
		}
		if at, ok := thread["recencyAt"].(float64); ok && (!hasRecency || at > newestAt) {
			newest, newestAt = id, at
			hasRecency = true
		}
	}
	if hasRecency {
		return newest
	}
	if rememberedFound {
		return remembered
	}
	return fallback
}

func rememberCodexThread(root, session, id string) error {
	return writeTextAtomic(codexThreadFile(root, session), id+"\n", 0600)
}

// CodexStartTurn sends one message to the thread the terminal is showing and
// waits for the turn to finish, so a caller learns the agent actually accepted
// it rather than only that a keystroke was written.
var CodexStartTurn = func(ctx context.Context, session, root, workspace, text string, images []string) error {
	client, err := dialCodexAppServer(ctx, session)
	if err != nil {
		return err
	}
	defer client.Close()
	thread, err := CodexCurrentThread(ctx, client, root, session, workspace)
	if err != nil {
		return err
	}
	input := []map[string]any{{"type": "text", "text": text}}
	for _, path := range images {
		input = append(input, map[string]any{"type": "image", "path": path})
	}
	if _, err := client.call(ctx, "turn/start", map[string]any{"threadId": thread, "input": input}); err != nil {
		return err
	}
	return nil
}

// CodexAppServerReady reports whether the session's app server is accepting
// connections, the way openCodeReadyProbe does for OpenCode.
var CodexAppServerReady = func(ctx context.Context, session string) (bool, error) {
	client, err := dialCodexAppServer(ctx, session)
	if err != nil {
		return false, nil
	}
	client.Close()
	return true, nil
}

// codexAppServerPrefix marks the tmux session holding a session's app server.
// The vmbox-internal- prefix keeps it out of the desktop's viewer windows, and
// tmux owning it means it dies with the box rather than outliving it.
const codexAppServerPrefix = "vmbox-internal-codex-"

func codexAppServerSession(session string) string { return codexAppServerPrefix + session }

// EnsureCodexAppServer starts the app server the terminal and chat both talk to,
// and waits for it to accept a connection. Starting it before the terminal
// matters: the terminal attaches with --remote and has nothing to attach to
// otherwise.
var EnsureCodexAppServer = func(ctx context.Context, session string) error {
	name := codexAppServerSession(session)
	if _, err := tmuxCommand(ctx, "", "has-session", "-t", name); err != nil {
		argv := []string{"new-session", "-d", "-s", name, "-c", WorkspaceDirectory(), "--",
			"codex", "app-server", "--listen", fmt.Sprintf("ws://127.0.0.1:%d", CodexChatPort(session))}
		if _, err := tmuxCommand(ctx, "", argv...); err != nil {
			return fmt.Errorf("start codex app server: %w", err)
		}
	}
	deadline := time.Now().Add(agentReadyTimeout)
	for {
		ready, err := CodexAppServerReady(ctx, session)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("codex app server did not start on port %d", CodexChatPort(session))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(agentReadyPollInterval):
		}
	}
}
