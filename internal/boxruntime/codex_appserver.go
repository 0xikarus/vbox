package boxruntime

import (
	"context"
	"encoding/base64"
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

func codexResetPendingFile(root, session string) string {
	return filepath.Join(root, "chat", "codex-reset-pending", session)
}

func codexFreshUsedFile(root, session string) string {
	return filepath.Join(root, "chat", "codex-fresh-used", session)
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

// CodexCurrentThread reports the most recently active persisted thread for the
// workspace. A new TUI's first message must enter its visible pane first;
// thread/list can omit that idle thread and still return old rollouts.
func CodexCurrentThread(ctx context.Context, client *codexClient, root, session, workspace string) (string, error) {
	if data, err := os.ReadFile(codexResetPendingFile(root, session)); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			if id == "visible-tui" {
				return "", fmt.Errorf("fresh Codex TUI needs its first message through the visible pane")
			}
			return id, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
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
	// A new thread created here would not be attached to the visible TUI.
	return "", fmt.Errorf("visible Codex thread is not available yet")
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

// A new remote TUI owns a zero-turn thread that Codex may not have persisted
// yet. Record its birth rather than starting an app-server thread from a second
// client: a second client's zero-turn ID can disappear before the TUI attaches.
func markFreshCodexTUI(root, session string) error {
	if err := writeTextAtomic(codexResetPendingFile(root, session), "visible-tui\n", 0600); err != nil {
		return err
	}
	if err := os.Remove(codexFreshUsedFile(root, session)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(codexThreadFile(root, session)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// codexRecentRolloutThread finds a thread with an actual rollout modified
// since the fresh TUI appeared. A zero-turn ID returned by thread/list alone
// cannot accept queue/add on Codex releases that have not persisted it yet.
var codexRecentRolloutThread = func(ctx context.Context, session, home string, since time.Time) (string, error) {
	client, err := dialCodexAppServer(ctx, session)
	if err != nil {
		return "", err
	}
	defer client.Close()
	result, err := client.call(ctx, "thread/list", map[string]any{})
	if err != nil {
		return "", err
	}
	threads, _ := result["data"].([]any)
	newest := ""
	var newestAt time.Time
	for _, value := range threads {
		thread, _ := value.(map[string]any)
		id, _ := thread["id"].(string)
		if id == "" {
			continue
		}
		paths, err := filepath.Glob(filepath.Join(home, ".codex", "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl"))
		if err != nil {
			return "", err
		}
		for _, path := range paths {
			info, err := os.Stat(path)
			if err == nil && info.ModTime().After(since) && info.ModTime().After(newestAt) {
				newest, newestAt = id, info.ModTime()
			}
		}
	}
	return newest, nil
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
	start := func(input []map[string]any) error {
		_, err := client.call(ctx, "turn/start", map[string]any{"threadId": thread, "input": input})
		return err
	}
	startFresh := func() error {
		result, err := client.call(ctx, "thread/start", map[string]any{"cwd": workspace})
		if err != nil {
			return err
		}
		started, _ := result["thread"].(map[string]any)
		id, _ := started["id"].(string)
		if id == "" {
			return fmt.Errorf("codex app server returned no fresh thread")
		}
		if err := rememberCodexThread(root, session, id); err != nil {
			return err
		}
		if err := writeTextAtomic(codexResetPendingFile(root, session), id+"\n", 0600); err != nil {
			return err
		}
		thread = id
		return nil
	}
	if err := codexStartTurnWithThreadRecovery(text, images, start, startFresh); err != nil {
		return err
	}
	// A successful turn makes the fresh thread active. Later turns may once
	// again follow a different conversation selected in the visible terminal.
	_ = os.Remove(codexResetPendingFile(root, session))
	return nil
}

// CodexQueueMessage asks the app server to enqueue one structured user input
// for its loaded visible thread. Queueing is shared with the remote TUI, unlike
// starting a turn from an independent app-server client.
var CodexQueueMessage = func(ctx context.Context, session, root, workspace, messageID, text string, images []string) error {
	if err := waitForAgentReady(ctx, session, "codex"); err != nil {
		return err
	}
	client, err := dialCodexAppServer(ctx, session)
	if err != nil {
		return err
	}
	defer client.Close()
	thread, err := CodexCurrentThread(ctx, client, root, session, workspace)
	if err != nil {
		return err
	}
	if err := codexQueueInput(ctx, client, thread, messageID, text, images); err != nil {
		return err
	}
	_ = os.Remove(codexResetPendingFile(root, session))
	return nil
}

func codexQueueInput(ctx context.Context, client *codexClient, thread, messageID, text string, images []string) error {
	add := func(input []map[string]any) error {
		result, err := client.call(ctx, "thread/queue/add", map[string]any{
			"threadId": thread, "input": input, "clientUserMessageId": messageID,
		})
		if err != nil {
			return err
		}
		queued, _ := result["queuedSubmission"].(map[string]any)
		if queued["id"] == nil {
			return fmt.Errorf("codex app server did not confirm queued message")
		}
		return nil
	}
	return codexStartTurnWithFallback(text, images, add)
}

// A restarted app server can list a thread stored on disk without loading it
// for turns. The explicit "thread not found" response proves the turn never
// began, so starting one fresh thread and retrying once cannot duplicate it.
func codexStartTurnWithThreadRecovery(text string, images []string, start func([]map[string]any) error, startFresh func() error) error {
	err := codexStartTurnWithFallback(text, images, start)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "thread not found") {
		return err
	}
	if err := startFresh(); err != nil {
		return err
	}
	return codexStartTurnWithFallback(text, images, start)
}

func codexStartTurnWithFallback(text string, images []string, start func([]map[string]any) error) error {
	if err := start(codexTurnInput(text, images)); err != nil {
		if len(images) == 0 || !codexNeedsImageURLFallback(err) {
			return err
		}
		input, inputErr := codexImageURLTurnInput(text, images)
		if inputErr != nil {
			return inputErr
		}
		return start(input)
	}
	return nil
}

func codexTurnInput(text string, images []string) []map[string]any {
	input := []map[string]any{{"type": "text", "text": text}}
	for _, path := range images {
		// App-server distinguishes localImage/path from the image/url variant.
		input = append(input, map[string]any{"type": "localImage", "path": path})
	}
	return input
}

// Older app-server releases predate localImage/path and deserialize that shape
// as image/url, producing "missing field `url`". A rejected request has not
// started a turn, so retrying with the older structured image item is safe and
// keeps retained workers compatible. The data URL is the item's byte transport;
// it is not inserted into the text prompt.
func codexNeedsImageURLFallback(err error) bool {
	message := err.Error()
	return strings.Contains(message, "missing field `url`") || strings.Contains(message, "unknown variant `localImage`")
}

func codexImageURLTurnInput(text string, images []string) ([]map[string]any, error) {
	input := []map[string]any{{"type": "text", "text": text}}
	for _, path := range images {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read Codex image fallback: %w", err)
		}
		media, err := validateChatImage(data)
		if err != nil {
			return nil, fmt.Errorf("validate Codex image fallback: %w", err)
		}
		input = append(input, map[string]any{
			"type": "image",
			"url":  "data:" + media + ";base64," + base64.StdEncoding.EncodeToString(data),
		})
	}
	return input, nil
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
	if ready, err := CodexAppServerReady(ctx, session); err != nil {
		return err
	} else if ready {
		return nil
	}
	// Old snapshots restored this internal helper as an interrupted login
	// shell. A tmux name alone does not prove that the app server is alive.
	if _, err := tmuxCommand(ctx, "", "has-session", "-t", "="+name); err == nil {
		command, commandErr := tmuxCommand(ctx, "", "display-message", "-p", "-t", "="+name+":0.0", "#{pane_current_command}")
		if commandErr == nil && (strings.TrimSpace(string(command)) == "bash" || strings.TrimSpace(string(command)) == "sh") {
			if _, err := tmuxCommand(ctx, "", "kill-session", "-t", "="+name); err != nil {
				return fmt.Errorf("remove stale codex app server session: %w", err)
			}
		}
	}
	if _, err := tmuxCommand(ctx, "", "has-session", "-t", "="+name); err != nil {
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
