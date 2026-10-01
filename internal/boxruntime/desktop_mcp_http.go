package boxruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DesktopMCPPort maps a worker assignment to the loopback port the box's HTTP
// tool façade listens on. Boxes on a shared worker see each other's loopback, so
// the port is derived per assignment the way the chat ports are per session.
func DesktopMCPPort(assignment string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte("desktop-mcp-http:" + assignment))
	return 40_000 + int(hash.Sum32()%10_000)
}

func desktopMCPHTTPURL(assignment string) string {
	return fmt.Sprintf("http://127.0.0.1:%d", DesktopMCPPort(assignment))
}

func desktopMCPVaultDir(home string) string {
	return filepath.Join(home, ".local", "share", "vmbox")
}

// desktopMCPEndpointFile is where a script finds the façade, so an agent can
// point one at it without being handed the address and token.
func desktopMCPEndpointFile(home string) string {
	return filepath.Join(desktopMCPVaultDir(home), "mcp-http.json")
}

// EnsureDesktopMCPToken returns the box's façade token, minting one on first
// use. Loopback is shared between boxes on a worker, so the token is what keeps
// one box's tools out of another's reach.
func EnsureDesktopMCPToken(home string) (string, error) {
	path := filepath.Join(desktopMCPVaultDir(home), "mcp-http-token")
	if data, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(data)); len(token) >= 32 {
			return token, nil
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	return token, writeTextAtomic(path, token+"\n", 0600)
}

func writeDesktopMCPEndpoint(home, assignment, token string) error {
	url := desktopMCPHTTPURL(assignment)
	encoded, err := json.MarshalIndent(map[string]string{"url": url, "promptUrl": url + "/prompt", "token": token}, "", "  ")
	if err != nil {
		return err
	}
	return writeTextAtomic(desktopMCPEndpointFile(home), string(encoded)+"\n", 0600)
}

// ServeDesktopMCPHTTP exposes the desktop tools over HTTP for scripts running
// inside the box. It is the same dispatch the MCP clients reach, so a background
// job can queue a chat message without an agent turn.
func ServeDesktopMCPHTTP(ctx context.Context, assignment, home string) error {
	token, err := EnsureDesktopMCPToken(home)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", DesktopMCPPort(assignment)))
	if err != nil {
		return fmt.Errorf("desktop tool server: %w", err)
	}
	if err := writeDesktopMCPEndpoint(home, assignment, token); err != nil {
		_ = listener.Close()
		return err
	}
	go runLocalHeartbeats(ctx, assignment, home, token)
	go runLocalMCPActivity(ctx, assignment, home)
	server := &http.Server{Handler: desktopMCPHTTPHandler(assignment, token), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func desktopMCPHTTPHandler(assignment, token string, resolvers ...desktopToolPolicyResolver) http.Handler {
	resolve := desktopToolPolicyResolver(desktopAgentToolPolicy)
	if len(resolvers) > 0 && resolvers[0] != nil {
		resolve = resolvers[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeDesktopMCPJSON(writer, http.StatusOK, map[string]any{"ok": true})
	})
	mux.Handle("/tools", desktopMCPAuthorized(token, func(writer http.ResponseWriter, request *http.Request) {
		tools, _, err := allowedDesktopMCPTools(request.Context(), assignment, resolve)
		if err != nil {
			writeDesktopMCPError(writer, http.StatusServiceUnavailable, "MCP tool policy unavailable")
			return
		}
		writeDesktopMCPJSON(writer, http.StatusOK, map[string]any{"tools": tools})
	}))
	mux.Handle("/prompt", desktopMCPAuthorized(token, localAgentPromptHandler()))
	mux.Handle("/tools/", desktopMCPAuthorized(token, desktopMCPCallHandler(assignment, resolve)))
	mux.Handle("/", desktopMCPAuthorized(token, desktopMCPIndexHandler(assignment, resolve)))
	return mux
}

func desktopMCPAuthorized(token string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		presented := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		if presented == "" {
			presented = request.Header.Get("X-Vmbox-Token")
		}
		if subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
			writeDesktopMCPError(writer, http.StatusUnauthorized, "send the token from mcp-http.json as an Authorization: Bearer header")
			return
		}
		next(writer, request)
	})
}

func desktopMCPIndexHandler(assignment string, resolve desktopToolPolicyResolver) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			writeDesktopMCPError(writer, http.StatusNotFound, "no such endpoint")
			return
		}
		tools, _, err := allowedDesktopMCPTools(request.Context(), assignment, resolve)
		if err != nil {
			writeDesktopMCPError(writer, http.StatusServiceUnavailable, "MCP tool policy unavailable")
			return
		}
		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			names = append(names, tool["name"].(string))
		}
		writeDesktopMCPJSON(writer, http.StatusOK, map[string]any{
			"server": "vmbox-desktop",
			"tools":  names,
			"usage": map[string]string{
				"list":    "GET /tools",
				"call":    "POST /tools/{name} with a JSON object of arguments, or GET /tools/{name}?argument=value",
				"prompt":  "POST /prompt with {\"text\":\"...\"} to deliver a user message to the running agent conversation",
				"auth":    "Authorization: Bearer <token from ~/.local/share/vmbox/mcp-http.json>",
				"session": "prompt, set_busy, chat_message and chat_ask target the sole agent conversation; name another with an X-Vmbox-Session header",
			},
		})
	}
}

func localAgentPromptHandler() http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			writeDesktopMCPError(writer, http.StatusMethodNotAllowed, "use POST")
			return
		}
		var input struct {
			Text      string `json:"text"`
			Session   string `json:"session,omitempty"`
			MessageID string `json:"messageId,omitempty"`
		}
		decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.Text) == "" || len(input.Text) > 140_000 {
			writeDesktopMCPError(writer, http.StatusBadRequest, "send one JSON object with non-empty text up to 140000 bytes")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeDesktopMCPError(writer, http.StatusBadRequest, "send one JSON object with non-empty text up to 140000 bytes")
			return
		}
		session := strings.TrimSpace(request.Header.Get("X-Vmbox-Session"))
		if session == "" {
			session = strings.TrimSpace(input.Session)
		} else if input.Session != "" && input.Session != session {
			writeDesktopMCPError(writer, http.StatusBadRequest, "session body and X-Vmbox-Session disagree")
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 2*time.Minute)
		defer cancel()
		var err error
		if session == "" {
			session, err = soleAgentConversation(ctx)
			if err != nil {
				writeDesktopMCPError(writer, http.StatusConflict, err.Error())
				return
			}
		}
		if err := validateTmuxToken("session", session); err != nil {
			writeDesktopMCPError(writer, http.StatusBadRequest, err.Error())
			return
		}
		marker, err := tmuxCommand(ctx, "", "show-environment", "-t", session, taskAgentEnvironment)
		if err != nil {
			writeDesktopMCPError(writer, http.StatusConflict, "agent conversation is not running")
			return
		}
		agent := strings.TrimPrefix(strings.TrimSpace(string(marker)), taskAgentEnvironment+"=")
		home, err := os.UserHomeDir()
		if err != nil {
			writeDesktopMCPError(writer, http.StatusInternalServerError, "box home is unavailable")
			return
		}
		messageID := input.MessageID
		if messageID == "" {
			messageID = ID("local_")
		} else if err := validateTmuxToken("messageId", messageID); err != nil {
			writeDesktopMCPError(writer, http.StatusBadRequest, err.Error())
			return
		}
		inbound := ChatInbound{ID: messageID, Text: input.Text}
		switch agent {
		case "codex":
			err = DeliverCodexChat(ctx, New("").Root, home, session, inbound)
		case "claude":
			err = DeliverClaudeChat(ctx, home, session, inbound)
		case "opencode":
			err = DeliverOpenCodeChat(ctx, home, session, inbound)
		default:
			err = fmt.Errorf("session is not a managed agent conversation")
		}
		if err != nil {
			writeDesktopMCPError(writer, http.StatusConflict, err.Error())
			return
		}
		writeDesktopMCPJSON(writer, http.StatusAccepted, map[string]any{"accepted": true, "session": session, "messageId": inbound.ID})
	}
}

func desktopMCPCallHandler(assignment string, resolve desktopToolPolicyResolver) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		name := strings.TrimPrefix(request.URL.Path, "/tools/")
		if name == "" || strings.Contains(name, "/") {
			writeDesktopMCPError(writer, http.StatusNotFound, "unknown desktop tool")
			return
		}
		known := false
		for _, tool := range desktopMCPTools() {
			known = known || tool["name"] == name
		}
		if !known {
			writeDesktopMCPError(writer, http.StatusBadRequest, "unknown desktop tool")
			return
		}
		query := request.URL.Query()
		session := request.Header.Get("X-Vmbox-Session")
		if session == "" {
			session = query.Get("vmbox_session")
		}
		query.Del("vmbox_session")
		arguments, status, err := desktopMCPArguments(request, query, name)
		if err != nil {
			_ = queueLocalMCPActivity(assignment, name, arguments, err)
			writeDesktopMCPError(writer, status, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), desktopToolTimeout(name))
		defer cancel()
		_, allowed, err := allowedDesktopMCPTools(ctx, assignment, resolve)
		if err != nil {
			_ = queueLocalMCPActivity(assignment, name, arguments, err)
			writeDesktopMCPError(writer, http.StatusServiceUnavailable, "MCP tool policy unavailable")
			return
		}
		if !allowed[name] {
			_ = queueLocalMCPActivity(assignment, name, arguments, fmt.Errorf("MCP tool is not allowed for this box"))
			writeDesktopMCPError(writer, http.StatusForbidden, "MCP tool is not allowed for this box")
			return
		}
		var heartbeatRequest struct {
			Action string `json:"action"`
		}
		if name == "heartbeat" {
			_ = json.Unmarshal(arguments, &heartbeatRequest)
		}
		if desktopMCPChatTools[name] || (name == "heartbeat" && heartbeatRequest.Action == "start") {
			if session == "" {
				if session, err = soleAgentConversation(ctx); err != nil {
					_ = queueLocalMCPActivity(assignment, name, arguments, err)
					writeDesktopMCPError(writer, http.StatusConflict, err.Error())
					return
				}
			}
			ctx = WithChatSession(ctx, session)
		}
		result, err := callDesktopTool(ctx, assignment, name, arguments)
		_ = queueLocalMCPActivity(assignment, name, arguments, err)
		if err != nil {
			writeDesktopMCPError(writer, http.StatusBadRequest, err.Error())
			return
		}
		if session != "" {
			result["session"] = session
		}
		writeDesktopMCPJSON(writer, http.StatusOK, result)
	}
}

// desktopMCPChatTools names the tools that write into a conversation. The
// facade serves the whole box from one process, so it has to say which
// conversation rather than letting the writer infer it from its own tmux
// session, which is the facade's own.
var desktopMCPChatTools = map[string]bool{"set_busy": true, "chat_message": true, "chat_ask": true}

// soleAgentConversation names the box's agent conversation when there is
// exactly one. With several, the caller has to choose: guessing would post a
// script's message into somebody else's thread.
func soleAgentConversation(ctx context.Context) (string, error) {
	listed, err := tmuxCommand(ctx, "", "list-sessions", "-F", "#{session_name}")
	if err != nil {
		return "", fmt.Errorf("no agent conversation is running")
	}
	var conversations []string
	for _, name := range strings.Fields(string(listed)) {
		if strings.HasPrefix(name, "vmbox-internal-") || validateTmuxToken("session", name) != nil {
			continue
		}
		marker, err := tmuxCommand(ctx, "", "show-environment", "-t", name, taskAgentEnvironment)
		if err != nil {
			continue
		}
		agent := strings.TrimPrefix(strings.TrimSpace(string(marker)), taskAgentEnvironment+"=")
		if agent == "codex" || agent == "claude" || agent == "opencode" {
			conversations = append(conversations, name)
		}
	}
	switch len(conversations) {
	case 0:
		return "", fmt.Errorf("no agent conversation is running")
	case 1:
		return conversations[0], nil
	default:
		return "", fmt.Errorf("name the conversation with X-Vmbox-Session; this box is running %s", strings.Join(conversations, ", "))
	}
}

func desktopMCPArguments(request *http.Request, query url.Values, name string) (json.RawMessage, int, error) {
	switch request.Method {
	case http.MethodGet:
		arguments, err := desktopMCPQueryArguments(query, name)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		return arguments, 0, nil
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
		if err != nil {
			return nil, http.StatusBadRequest, fmt.Errorf("could not read request body")
		}
		if len(bytes.TrimSpace(body)) == 0 {
			return json.RawMessage(`{}`), 0, nil
		}
		return json.RawMessage(body), 0, nil
	default:
		return nil, http.StatusMethodNotAllowed, fmt.Errorf("use GET or POST")
	}
}

// desktopMCPQueryArguments turns ?x=10&keys=ctrl&keys=l into the JSON the tool
// expects, using its schema to decide what each value should become.
func desktopMCPQueryArguments(query url.Values, name string) (json.RawMessage, error) {
	var properties map[string]any
	for _, tool := range desktopMCPTools() {
		if tool["name"] == name {
			properties = tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		}
	}
	if properties == nil {
		return nil, fmt.Errorf("unknown desktop tool")
	}
	values := map[string]any{}
	for key, list := range query {
		schema, ok := properties[key].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unknown tool argument")
		}
		kind, _ := schema["type"].(string)
		if kind == "array" {
			items := make([]any, 0, len(list))
			for _, item := range list {
				items = append(items, item)
			}
			values[key] = items
			continue
		}
		converted, err := desktopMCPScalar(kind, list[len(list)-1])
		if err != nil {
			return nil, fmt.Errorf("%s %w", key, err)
		}
		values[key] = converted
	}
	return json.Marshal(values)
}

func desktopMCPScalar(kind, raw string) (any, error) {
	switch kind {
	case "integer":
		number, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("must be a whole number")
		}
		return number, nil
	case "boolean":
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("must be true or false")
		}
		return value, nil
	default:
		return raw, nil
	}
}

func writeDesktopMCPJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeDesktopMCPError(writer http.ResponseWriter, status int, message string) {
	writeDesktopMCPJSON(writer, status, map[string]any{"error": message})
}

func desktopMCPHTTPSession() string { return "vmbox-internal-mcp-http" }

var desktopMCPHTTPReady = func(ctx context.Context, assignment string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, desktopMCPHTTPURL(assignment)+"/health", nil)
	if err != nil {
		return false
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

// EnsureDesktopMCPHTTP starts the box's HTTP tool façade and waits for it to
// answer, so a script the agent writes can reach the same tools its MCP client
// has.
var EnsureDesktopMCPHTTP = func(ctx context.Context, assignment string) error {
	if assignment == "" {
		return nil
	}
	// An image without the box runtime in HOME has no MCP server either, which
	// is reported where it is registered. Waiting for a facade that can never
	// start would block every agent launch behind it.
	if info, err := os.Stat(desktopRuntimePath()); err != nil || info.Mode()&0o111 == 0 {
		return nil
	}
	name := desktopMCPHTTPSession()
	if desktopMCPHTTPReady(ctx, assignment) {
		return nil
	}
	// A tmux snapshot can restore this well-known session as a shell instead of
	// the runtime command. Its name is not evidence that the server is alive;
	// replace any unready occupant before waiting on the port.
	if _, err := tmuxCommand(ctx, "", "has-session", "-t", "="+name); err == nil {
		if _, err := tmuxCommand(ctx, "", "kill-session", "-t", "="+name); err != nil {
			return fmt.Errorf("stop stale desktop tool server: %w", err)
		}
	}
	argv := []string{"new-session", "-d", "-s", name, "-c", WorkspaceDirectory(), "--", desktopRuntimePath(), "desktop-mcp-http"}
	if _, err := tmuxCommand(ctx, "", argv...); err != nil {
		return fmt.Errorf("start desktop tool server: %w", err)
	}
	deadline := time.Now().Add(agentReadyTimeout)
	for {
		if desktopMCPHTTPReady(ctx, assignment) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("desktop tool server did not start on port %d", DesktopMCPPort(assignment))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(agentReadyPollInterval):
		}
	}
}
