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

var codexVisibleThreadState = codexTUIProxyState

// CodexCurrentThread returns the thread selected on the TUI's own app-server
// connection. Persisted recency and loaded-list order cannot identify it.
func CodexCurrentThread(ctx context.Context, client *codexClient, root, session, workspace string) (string, error) {
	connected, id, err := codexVisibleThreadState(ctx, session)
	if err != nil {
		return "", fmt.Errorf("Codex TUI connection unavailable: %w", err)
	}
	if !connected {
		return "", fmt.Errorf("Codex TUI is disconnected; reopen it before sending chat")
	}
	if id == "" {
		return "", fmt.Errorf("Codex TUI has not selected a thread yet")
	}
	return id, nil
}

// CodexCompactVisibleThread targets the thread selected by the actual TUI.
// App-server acknowledges the request before the compaction turn finishes.
func CodexCompactVisibleThread(ctx context.Context, root, session string) error {
	client, err := dialCodexAppServer(ctx, session)
	if err != nil {
		return err
	}
	defer client.Close()
	thread, err := CodexCurrentThread(ctx, client, root, session, WorkspaceDirectory())
	if err != nil {
		return err
	}
	_, err = client.call(ctx, "thread/compact/start", map[string]any{"threadId": thread})
	return err
}

func rememberCodexThread(root, session, id string) error {
	return writeTextAtomic(codexThreadFile(root, session), id+"\n", 0600)
}

// A new remote TUI owns a zero-turn thread that Codex may not have persisted
// yet. Record its birth so an older saved thread cannot be used by mistake.
func markFreshCodexTUI(root, session string) error {
	if err := writeTextAtomic(codexResetPendingFile(root, session), "visible-tui\n", 0600); err != nil {
		return err
	}
	if err := os.Remove(codexThreadFile(root, session)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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
	if err := EnsureCodexTUIProxy(ctx, root, session); err != nil {
		return err
	}
	client, err := dialCodexAppServer(ctx, session)
	if err != nil {
		return err
	}
	defer client.Close()
	thread, err := waitForCodexVisibleThread(ctx, client, root, session, workspace)
	if err != nil {
		return err
	}
	if err := codexQueueInput(ctx, client, thread, messageID, text, images); err != nil {
		return err
	}
	// Queueing only stores the submission. After an interrupted turn Codex can
	// leave that queue idle until another client explicitly starts its head.
	if err := codexStartQueuedIfIdle(ctx, client, thread); err != nil {
		return fmt.Errorf("%w: Codex message queued but pending queue could not start: %v", ErrAmbiguousMessage, err)
	}
	if err := waitForCodexUserItem(ctx, client, thread, messageID); err != nil {
		return err
	}
	_ = os.Remove(codexResetPendingFile(root, session))
	return nil
}

// The remote TUI binds its own new thread after it connects. Wait for that
// binding through the proxy instead of parsing the rendered terminal screen.
func waitForCodexVisibleThread(ctx context.Context, client *codexClient, root, session, workspace string) (string, error) {
	deadline := time.NewTimer(agentReadyTimeout)
	defer deadline.Stop()
	for {
		thread, err := CodexCurrentThread(ctx, client, root, session, workspace)
		if err == nil {
			return thread, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("Codex TUI did not select a thread: %w", err)
		case <-time.After(agentReadyPollInterval):
		}
	}
}

// codexStartQueuedIfIdle starts the oldest native submission, including one
// queued before the current message. Never starts a second turn while the TUI
// already has one in progress. The queue itself preserves submission order.
func codexStartQueuedIfIdle(ctx context.Context, client *codexClient, thread string) error {
	active := func() (bool, error) {
		result, err := client.call(ctx, "thread/turns/list", map[string]any{"threadId": thread, "limit": 1, "sortDirection": "desc"})
		if err != nil {
			return false, err
		}
		turns, _ := result["data"].([]any)
		if len(turns) == 0 {
			return false, nil
		}
		latest, _ := turns[0].(map[string]any)
		return latest["status"] == "inProgress", nil
	}
	busy, err := active()
	if codexQueueMethodUnavailable(err, "thread/turns/list") {
		return nil
	}
	if err != nil || busy {
		return err
	}
	result, err := client.call(ctx, "thread/queue/list", map[string]any{"threadId": thread, "limit": 1})
	if codexQueueMethodUnavailable(err, "thread/queue/list") {
		return nil
	}
	if err != nil {
		return err
	}
	queue, _ := result["data"].([]any)
	if len(queue) == 0 {
		return nil
	}
	first, _ := queue[0].(map[string]any)
	id, _ := first["id"].(string)
	if id == "" {
		return fmt.Errorf("Codex queue returned no submission ID")
	}
	if _, err := client.call(ctx, "thread/queue/start", map[string]any{"threadId": thread, "queuedSubmissionId": id}); err != nil {
		if codexQueueMethodUnavailable(err, "thread/queue/start") {
			return nil
		}
		// Another client may have started the queue after our idle check.
		if running, checkErr := active(); checkErr == nil && running {
			return nil
		}
		return err
	}
	return nil
}

func codexQueueMethodUnavailable(err error, method string) bool {
	return err != nil && strings.Contains(err.Error(), "unknown variant `"+method+"`")
}

// ConfirmCodexChat probes an earlier ambiguous handoff without submitting it
// again. An idle native queue may be started, but its existing contents stay
// in their original order and the receipt is confirmed only after consumption.
func ConfirmCodexChat(ctx context.Context, root, home, session, messageID string) (bool, error) {
	client, err := dialCodexAppServer(ctx, session)
	if err != nil {
		return false, err
	}
	defer client.Close()
	thread, err := CodexCurrentThread(ctx, client, root, session, WorkspaceDirectory())
	if err != nil {
		return false, err
	}
	accepted, err := codexUserItemPresent(ctx, client, thread, messageID, true)
	if err != nil || !accepted {
		if err == nil {
			err = codexStartQueuedIfIdle(ctx, client, thread)
		}
		return false, err
	}
	path := filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", session, messageID+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return true, nil
}

var codexUserItemPollInterval = 250 * time.Millisecond
var codexQueueRetryInterval = 2 * time.Second

// A queue receipt only confirms storage. The matching native user item proves
// that this thread consumed the exact message before Chat shows it as delivered.
func waitForCodexUserItem(ctx context.Context, client *codexClient, thread, messageID string) error {
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	lastQueueStart := time.Now()
	for {
		accepted, err := codexUserItemPresent(ctx, client, thread, messageID, false)
		if err != nil {
			return fmt.Errorf("%w: Codex queue accepted but native consumption could not be checked: %v", ErrAmbiguousMessage, err)
		}
		if accepted {
			return nil
		}
		// An active turn can finish or be interrupted after the initial queue
		// start check. The TUI does not always advance its native queue then.
		// Keep starting only its oldest item when idle, so this delivery does
		// not have to wait for the controller's later receipt reconciliation.
		if time.Since(lastQueueStart) >= codexQueueRetryInterval {
			if err := codexStartQueuedIfIdle(ctx, client, thread); err != nil {
				return fmt.Errorf("%w: Codex message queued but pending queue could not restart: %v", ErrAmbiguousMessage, err)
			}
			lastQueueStart = time.Now()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: Codex queue accepted but native consumption was not confirmed: %v", ErrAmbiguousMessage, ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("%w: Codex queue accepted but native consumption was not confirmed within 90 seconds", ErrAmbiguousMessage)
		case <-time.After(codexUserItemPollInterval):
		}
	}
}

func codexUserItemPresent(ctx context.Context, client *codexClient, thread, messageID string, searchHistory bool) (bool, error) {
	result, err := client.call(ctx, "thread/items/list", map[string]any{"threadId": thread, "sortDirection": "desc", "limit": 100})
	if err == nil {
		entries, _ := result["data"].([]any)
		for _, value := range entries {
			entry, _ := value.(map[string]any)
			item, _ := entry["item"].(map[string]any)
			if item["type"] == "userMessage" && item["clientId"] == messageID {
				return true, nil
			}
		}
		if !searchHistory {
			return false, nil
		}
	}
	// A long Codex turn can push the user item outside the newest 100 items.
	result, err = client.call(ctx, "thread/read", map[string]any{"threadId": thread, "includeTurns": true})
	if err != nil {
		return false, err
	}
	threadData, _ := result["thread"].(map[string]any)
	turns, _ := threadData["turns"].([]any)
	for _, value := range turns {
		turn, _ := value.(map[string]any)
		items, _ := turn["items"].([]any)
		for _, value := range items {
			item, _ := value.(map[string]any)
			if item["type"] == "userMessage" && item["clientId"] == messageID {
				return true, nil
			}
		}
	}
	return false, nil
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
