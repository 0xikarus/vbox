package boxruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/0xikarus/vmbox-service/internal/secrets"
)

type desktopMCPRequest struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func desktopMCPTools() []map[string]any {
	integer := map[string]any{"type": "integer", "minimum": 0}
	point := map[string]any{"x": integer, "y": integer}
	makeTool := func(name, description string, properties map[string]any, required ...string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{"name": name, "description": description, "inputSchema": map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}
	}
	return []map[string]any{
		makeTool("get_contacts", "List the boxes this box is permitted to message. Returns each contact's id, name, role, agent, state and whether messaging is allowed. Use a contact id or name in chat_message or chat_ask. The controller enforces this list; you cannot message a box that is not returned here.", map[string]any{}),
		makeTool("get_run_budget", "Get this box's durable run-time budget. The countdown advances only while the box is allocated and is separate from desktop inactivity.", map[string]any{}),
		makeTool("request_more_time", "Request a bounded extension to this box's run-time budget. Reuse idempotencyKey when retrying.", map[string]any{"minutes": map[string]any{"type": "integer", "minimum": 1, "maximum": 1440}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "minutes", "idempotencyKey"),
		makeTool("queue_followup", "Queue a durable follow-up for this running assignment, optionally delayed. It does not wake the box or reset its run-time budget. Reuse idempotencyKey when retrying.", map[string]any{"text": map[string]any{"type": "string", "minLength": 1, "maxLength": 100000}, "delaySeconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 604800}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "text", "idempotencyKey"),
		makeTool("get_thread_history", "Read a paginated direct or shared-chat thread this box already has access to. Pass chatId for a shared-chat thread. A thread reference alone never grants access.", map[string]any{"threadId": map[string]any{"type": "string", "minLength": 36, "maxLength": 36}, "chatId": map[string]any{"type": "string", "minLength": 36, "maxLength": 36}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "before": map[string]any{"type": "string"}, "beforeId": map[string]any{"type": "string", "minLength": 36, "maxLength": 36}}, "threadId"),
		makeTool("discover_shared_chats", "Discover account shared chats when explicitly granted by an assigned role.", map[string]any{}),
		makeTool("read_shared_chat", "Read messages in a shared chat where this box is a member.", map[string]any{"chatId": map[string]any{"type": "string", "minLength": 36, "maxLength": 36}}, "chatId"),
		makeTool("create_shared_chat", "Create a shared chat and join it with every-message subscription. Reuse idempotencyKey when retrying.", map[string]any{"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 100}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "name", "idempotencyKey"),
		makeTool("subscribe_shared_chat", "Set this box's delivery mode: following (no automatic delivery), mentions, or every_message.", map[string]any{"chatId": map[string]any{"type": "string", "minLength": 36, "maxLength": 36}, "mode": map[string]any{"type": "string", "enum": []string{"following", "mentions", "every_message"}}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "chatId", "mode", "idempotencyKey"),
		makeTool("invite_to_shared_chat", "Invite an account box to a shared chat. New members start in following mode.", map[string]any{"chatId": map[string]any{"type": "string", "minLength": 36, "maxLength": 36}, "boxId": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "chatId", "boxId", "idempotencyKey"),
		makeTool("send_shared_chat_message", "Post a message or threaded reply to a shared chat. Delivery follows each member's subscription mode.", map[string]any{"chatId": map[string]any{"type": "string", "minLength": 36, "maxLength": 36}, "text": map[string]any{"type": "string", "minLength": 1, "maxLength": 100000}, "parentMessageId": map[string]any{"type": "string", "minLength": 36, "maxLength": 36}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "chatId", "text", "idempotencyKey"),
		makeTool("create_email_address", "Provision an email address through the account's configured provider within this box's role grant. Reuse idempotencyKey when retrying.", map[string]any{"domain": map[string]any{"type": "string", "minLength": 1, "maxLength": 253}, "addressType": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}, "localPart": map[string]any{"type": "string", "maxLength": 64}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "domain", "addressType", "idempotencyKey"),
		makeTool("create_agent_box", "Create an agent box on this box's provider within explicitly granted agent, disk, count, and starting-role limits. Reuse idempotencyKey when retrying.", map[string]any{"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 100}, "agent": map[string]any{"type": "string", "enum": []string{"codex", "claude", "opencode"}}, "diskGiB": map[string]any{"type": "integer", "minimum": 1, "maximum": 4096}, "roleIds": map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "string"}}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "name", "agent", "idempotencyKey"),
		makeTool("set_busy", "Report whether this agent is actively working. Submitted chat messages set busy automatically and chat_message/chat_ask clear it automatically; call this only to override activity outside that normal request/reply flow.", map[string]any{"busy": map[string]any{"type": "boolean"}}, "busy"),
		makeTool("chat_message", "Send a message to the vmbox Agent chat. Pass replyTo to answer a specific message; without it the message is delivered on its own. Call this once for each completed response, including any image files the user should receive. Pass contact (from get_contacts) to send a message to another box instead of the owner; contact messages cannot carry image files.", map[string]any{"text": map[string]any{"type": "string", "minLength": 1, "maxLength": 100000}, "replyTo": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "contact": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "files": map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string"}}}, "text"),
		makeTool("chat_ask", "Ask the user to choose one or more options in vmbox Agent chat when their decision is required. replyTo is optional; without it the question is delivered on its own. Pass contact (from get_contacts) to ask another box's agent instead of the owner.", map[string]any{"question": map[string]any{"type": "string", "minLength": 1, "maxLength": 4000}, "choices": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 500}}, "multiple": map[string]any{"type": "boolean"}, "replyTo": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "contact": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "question", "choices"),
		makeTool("secret_request", "Request an existing account password privately from the user for the focused HTTPS password field. Never generate a substitute. Call again to check readiness.", map[string]any{"key": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}}, "key"),
		makeTool("secret_ensure", "Create or reuse an encrypted password reference for a new account on the current password field's HTTPS origin. Never use this for an existing account's credential.", map[string]any{"length": map[string]any{"type": "integer", "minimum": 16, "maximum": 128}, "alphabet": map[string]any{"type": "string", "minLength": 32, "maxLength": 94}, "key": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}, "purpose": map[string]any{"type": "string", "enum": []string{"new_account_password"}}}, "key", "purpose"),
		makeTool("typeSecret", "Fill the focused password field using an existing secret reference. Does not reveal the password, generate a new one, or submit the form.", map[string]any{"key": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}}, "key"),
		makeTool("desktop_screenshot", "Capture this box's current desktop as a PNG image. Does not start or wake the desktop.", map[string]any{}),
		makeTool("capture_window", "Capture the visible screen area of an X11 window as PNG. Defaults to the active window; optionally supply window_id (decimal or 0x hexadecimal). Does not focus or raise windows. Overlapping windows appear in the capture; minimized windows are not supported. Returned x/y offsets map image coordinates to desktop coordinates.", map[string]any{"window_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 10}}),
		makeTool("desktop_move", "Move the cursor smoothly to a screen coordinate.", point, "x", "y"),
		makeTool("desktop_click", "Move to a coordinate and click. Button: 1 left, 2 middle, 3 right. Count 2 sends a double-click with a brief inter-click delay.", map[string]any{"x": integer, "y": integer, "button": map[string]any{"type": "integer", "minimum": 1, "maximum": 3}, "count": map[string]any{"type": "integer", "minimum": 1, "maximum": 2}}, "x", "y"),
		makeTool("desktop_drag", "Drag directly from x/y to toX/toY while holding the left button.", map[string]any{"x": integer, "y": integer, "toX": integer, "toY": integer}, "x", "y", "toX", "toY"),
		makeTool("desktop_scroll", "Scroll at a coordinate. Text is direction; count is 1–20 wheel steps.", map[string]any{"x": integer, "y": integer, "text": map[string]any{"type": "string", "enum": []string{"up", "down", "left", "right"}}, "count": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}}, "x", "y", "text"),
		makeTool("desktop_type", "Type ordinary literal text in the focused application. Use the secret service for credentials.", map[string]any{"text": map[string]any{"type": "string", "minLength": 1, "maxLength": 16384}}, "text"),
		makeTool("desktop_key", "Press a shortcut: keys contains modifiers and a key, e.g. [ctrl,l] or [Return].", map[string]any{"keys": map[string]any{"type": "array", "minItems": 1, "maxItems": 5, "items": map[string]any{"type": "string"}}}, "keys"),
	}
}

// ServeDesktopMCP is a local stdio adapter. stdout contains protocol only; the
// assignment captured at startup fences every subsequent operation.
func ServeDesktopMCP(ctx context.Context, assignment string, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 65536)
	encoder := json.NewEncoder(output)
	var outputMu sync.Mutex
	encode := func(value any) error {
		outputMu.Lock()
		defer outputMu.Unlock()
		return encoder.Encode(value)
	}
	var channelOnce sync.Once
	var claudeChannelClient bool
	for scanner.Scan() {
		var request desktopMCPRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if err = encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "Invalid JSON"}}); err != nil {
				return err
			}
			continue
		}
		if len(request.ID) == 0 {
			if request.Method == "notifications/initialized" && claudeChannelClient {
				// The client only registers notification handlers after the
				// initialize response. Waiting for its initialized notification
				// prevents a queued first message from being emitted too early.
				channelOnce.Do(func() { go serveClaudeChannel(ctx, encode) })
			}
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
				ClientInfo      struct {
					Name string `json:"name"`
				} `json:"clientInfo"`
			}
			_ = json.Unmarshal(request.Params, &params)
			claudeChannelClient = strings.Contains(strings.ToLower(params.ClientInfo.Name), "claude")
			version := params.ProtocolVersion
			if version != "2024-11-05" && version != "2025-03-26" && version != "2025-06-18" && version != "2025-11-25" {
				version = "2025-06-18"
			}
			response["result"] = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}, "experimental": map[string]any{"claude/channel": map[string]any{}}}, "serverInfo": map[string]any{"name": "vmbox-desktop", "version": "0.2.0"}, "instructions": "Messages from vmbox Agent chat arrive as channel messages. Use chat_message for every response the user should receive; pass replyTo to answer a specific message. Use chat_ask when the user must choose. Busy state is automatic for ordinary request/reply work; use set_busy only to report activity outside that flow. These tools are also reachable over HTTP from inside this box: read ~/.local/share/vmbox/mcp-http.json for the url and token, then POST a JSON object of arguments to {url}/tools/{name} with an Authorization: Bearer header. Use that when a script or background job has to queue a message outside an agent turn."}
		case "ping":
			response["result"] = map[string]any{}
		case "tools/list":
			response["result"] = map[string]any{"tools": desktopMCPTools()}
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				response["error"] = map[string]any{"code": -32602, "message": "Invalid tool parameters"}
				break
			}
			callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			result, err := callDesktopTool(callCtx, assignment, params.Name, params.Arguments)
			cancel()
			if err != nil {
				result = map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}}
			}
			response["result"] = result
		default:
			response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
		}
		if err := encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func serveClaudeChannel(ctx context.Context, encode func(any) error) {
	session, err := chatSession(ctx)
	if err != nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	readyDir := claudeChannelReadyDir(home)
	if os.MkdirAll(readyDir, 0700) != nil {
		return
	}
	owner := ID("channel_")
	ready := claudeChannelReadyPath(home, session, owner)
	defer os.Remove(ready)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		_ = os.WriteFile(ready, []byte(owner), 0600)
		if event, path, found, err := nextChatInbound(home, session); err == nil && found {
			content := event.Text
			if len(event.Paths) > 0 {
				content += "\n\nAttached image files:\n" + strings.Join(event.Paths, "\n")
			}
			meta := map[string]any{"chat_id": session, "message_id": event.ID, "user": "vmbox-user", "ts": time.Now().UTC().Format(time.RFC3339Nano)}
			if len(event.Paths) > 0 {
				meta["file_path"] = event.Paths[0]
			}
			if encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/claude/channel", "params": map[string]any{"content": content, "meta": meta}}) == nil {
				_ = os.Remove(path)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func claudeChannelReadyDir(home string) string {
	return filepath.Join(home, ".local", "share", "vmbox", "chat", "channel-ready")
}

func claudeChannelReadyPath(home, session, owner string) string {
	return filepath.Join(claudeChannelReadyDir(home), session+"."+owner)
}

func claudeChannelOwners(home, session string) (map[string]struct{}, error) {
	owners := map[string]struct{}{}
	entries, err := os.ReadDir(claudeChannelReadyDir(home))
	if errors.Is(err, os.ErrNotExist) {
		return owners, nil
	}
	if err != nil {
		return nil, err
	}
	prefix := session + ".channel_"
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			owners[strings.TrimPrefix(entry.Name(), session+".")] = struct{}{}
		}
	}
	return owners, nil
}

func callDesktopTool(ctx context.Context, assignment, name string, args json.RawMessage) (map[string]any, error) {
	var tool map[string]any
	for _, candidate := range desktopMCPTools() {
		if candidate["name"] == name {
			tool = candidate
			break
		}
	}
	if tool == nil {
		return nil, fmt.Errorf("unknown desktop tool")
	}
	var values map[string]json.RawMessage
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(args, &values); err != nil || values == nil {
		return nil, fmt.Errorf("arguments must be an object")
	}
	schema := tool["inputSchema"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	for key := range values {
		if _, ok := properties[key]; !ok {
			return nil, fmt.Errorf("unknown tool argument")
		}
	}
	for _, key := range schema["required"].([]string) {
		if v, ok := values[key]; !ok || string(v) == "null" {
			return nil, fmt.Errorf("missing required argument: %s", key)
		}
	}
	if name == "get_contacts" {
		contacts, err := DesktopContacts(ctx, assignment)
		if err != nil {
			return nil, err
		}
		if len(contacts) == 0 {
			return map[string]any{"content": []map[string]any{{"type": "text", "text": "No contacts are available. The account owner has not granted this box any contact permission."}}}, nil
		}
		var lines []string
		for _, contact := range contacts {
			state := contact.State
			if state == "" {
				state = "unknown"
			}
			roleNames := make([]string, 0, len(contact.Roles))
			for _, role := range contact.Roles {
				roleNames = append(roleNames, role.Name)
			}
			roles := strings.Join(roleNames, ", ")
			if roles == "" {
				roles = "none"
			}
			lines = append(lines, fmt.Sprintf("- %s | id %s | roles %s | agent %s | %s | message %t", contact.Name, contact.ID, roles, contact.Agent, state, contact.CanMessage))
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "Contacts you may message:\n" + strings.Join(lines, "\n")}}}, nil
	}
	if name == "get_run_budget" {
		var budget map[string]any
		if err := desktopAgentAPI(ctx, assignment, http.MethodGet, "/v1/agent-desktop/run-budget", nil, &budget); err != nil {
			return nil, err
		}
		return desktopToolJSON(budget)
	}
	if name == "request_more_time" {
		var request struct {
			Minutes        int    `json:"minutes"`
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if json.Unmarshal(args, &request) != nil || request.Minutes < 1 || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("minutes and idempotencyKey are required")
		}
		var budget map[string]any
		if err := desktopAgentAPIWithKey(ctx, assignment, http.MethodPost, "/v1/agent-desktop/run-budget/extend", request.IdempotencyKey, map[string]any{"minutes": request.Minutes}, &budget); err != nil {
			return nil, err
		}
		return desktopToolJSON(budget)
	}
	if name == "queue_followup" {
		var request struct {
			Text           string `json:"text"`
			DelaySeconds   int    `json:"delaySeconds"`
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Text) == "" || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("text and idempotencyKey are required")
		}
		var followup map[string]any
		body := map[string]any{"text": request.Text, "delaySeconds": request.DelaySeconds}
		if err := desktopAgentAPIWithKey(ctx, assignment, http.MethodPost, "/v1/agent-desktop/followups", request.IdempotencyKey, body, &followup); err != nil {
			return nil, err
		}
		return desktopToolJSON(followup)
	}
	if name == "get_thread_history" {
		var request struct {
			ThreadID string `json:"threadId"`
			ChatID   string `json:"chatId"`
			Limit    int    `json:"limit"`
			Before   string `json:"before"`
			BeforeID string `json:"beforeId"`
		}
		if json.Unmarshal(args, &request) != nil || request.ThreadID == "" {
			return nil, fmt.Errorf("threadId is required")
		}
		if request.Limit == 0 {
			request.Limit = 50
		}
		query := "?threadId=" + url.QueryEscape(request.ThreadID) + "&limit=" + strconv.Itoa(request.Limit)
		if request.ChatID != "" {
			query += "&chatId=" + url.QueryEscape(request.ChatID)
		}
		if request.Before != "" || request.BeforeID != "" {
			query += "&before=" + url.QueryEscape(request.Before) + "&beforeId=" + url.QueryEscape(request.BeforeID)
		}
		var history map[string]any
		if err := desktopAgentAPI(ctx, assignment, http.MethodGet, "/v1/agent-desktop/thread-history"+query, nil, &history); err != nil {
			return nil, err
		}
		return desktopToolJSON(history)
	}
	if name == "discover_shared_chats" {
		var result map[string]any
		if err := desktopAgentAPI(ctx, assignment, http.MethodGet, "/v1/agent-desktop/shared-chats", nil, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "read_shared_chat" {
		var request struct {
			ChatID string `json:"chatId"`
		}
		if json.Unmarshal(args, &request) != nil || request.ChatID == "" {
			return nil, fmt.Errorf("chatId is required")
		}
		var result map[string]any
		if err := desktopAgentAPI(ctx, assignment, http.MethodGet, "/v1/agent-desktop/shared-chats/"+url.PathEscape(request.ChatID)+"/messages", nil, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "create_shared_chat" {
		var request struct {
			Name           string `json:"name"`
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Name) == "" || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("name and idempotencyKey are required")
		}
		var result map[string]any
		if err := desktopAgentAPIWithKey(ctx, assignment, http.MethodPost, "/v1/agent-desktop/shared-chats", request.IdempotencyKey, map[string]any{"name": request.Name}, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "subscribe_shared_chat" {
		var request struct {
			ChatID         string `json:"chatId"`
			Mode           string `json:"mode"`
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if json.Unmarshal(args, &request) != nil || request.ChatID == "" || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("chatId, mode, and idempotencyKey are required")
		}
		var result map[string]any
		if err := desktopAgentAPIWithKey(ctx, assignment, http.MethodPost, "/v1/agent-desktop/shared-chats/"+url.PathEscape(request.ChatID)+"/subscribe", request.IdempotencyKey, map[string]any{"mode": request.Mode}, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "invite_to_shared_chat" {
		var request struct {
			ChatID         string `json:"chatId"`
			BoxID          string `json:"boxId"`
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if json.Unmarshal(args, &request) != nil || request.ChatID == "" || request.BoxID == "" || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("chatId, boxId, and idempotencyKey are required")
		}
		var result map[string]any
		if err := desktopAgentAPIWithKey(ctx, assignment, http.MethodPost, "/v1/agent-desktop/shared-chats/"+url.PathEscape(request.ChatID)+"/invite", request.IdempotencyKey, map[string]any{"boxId": request.BoxID}, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "send_shared_chat_message" {
		var request struct {
			ChatID          string `json:"chatId"`
			Text            string `json:"text"`
			ParentMessageID string `json:"parentMessageId"`
			IdempotencyKey  string `json:"idempotencyKey"`
		}
		if json.Unmarshal(args, &request) != nil || request.ChatID == "" || strings.TrimSpace(request.Text) == "" || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("chatId, text, and idempotencyKey are required")
		}
		var result map[string]any
		if err := desktopAgentAPIWithKey(ctx, assignment, http.MethodPost, "/v1/agent-desktop/shared-chats/"+url.PathEscape(request.ChatID)+"/messages", request.IdempotencyKey, map[string]any{"text": request.Text, "parentMessageId": request.ParentMessageID}, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "create_email_address" {
		var request struct {
			Domain         string `json:"domain"`
			AddressType    string `json:"addressType"`
			LocalPart      string `json:"localPart"`
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if json.Unmarshal(args, &request) != nil || request.Domain == "" || request.AddressType == "" || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("domain, addressType, and idempotencyKey are required")
		}
		var result map[string]any
		if err := desktopAgentAPIWithKey(ctx, assignment, http.MethodPost, "/v1/agent-desktop/email-addresses", request.IdempotencyKey, map[string]any{"domain": request.Domain, "addressType": request.AddressType, "localPart": request.LocalPart}, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "create_agent_box" {
		var request struct {
			Name           string   `json:"name"`
			Agent          string   `json:"agent"`
			DiskGiB        int      `json:"diskGiB"`
			RoleIDs        []string `json:"roleIds"`
			IdempotencyKey string   `json:"idempotencyKey"`
		}
		if json.Unmarshal(args, &request) != nil || request.Name == "" || request.Agent == "" || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("name, agent, and idempotencyKey are required")
		}
		var result map[string]any
		if err := desktopAgentAPIWithKey(ctx, assignment, http.MethodPost, "/v1/agent-desktop/boxes", request.IdempotencyKey, map[string]any{"name": request.Name, "agent": request.Agent, "diskGiB": request.DiskGiB, "roleIds": request.RoleIDs}, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "set_busy" {
		var request struct {
			Busy *bool `json:"busy"`
		}
		if json.Unmarshal(args, &request) != nil || request.Busy == nil {
			return nil, fmt.Errorf("busy must be true or false")
		}
		session, err := chatSession(ctx)
		if err != nil {
			return nil, err
		}
		if err := DesktopSetBusy(ctx, assignment, session, *request.Busy); err != nil {
			return nil, err
		}
		state := "idle"
		if *request.Busy {
			state = "busy"
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "Agent activity is now " + state + "."}}}, nil
	}
	if name == "secret_request" {
		var key string
		if json.Unmarshal(values["key"], &key) != nil {
			return nil, fmt.Errorf("provide a secret reference")
		}
		ready, err := desktopSecretOperation(ctx, assignment, key, true, secrets.PasswordPolicy{}, "request")
		if err != nil {
			return nil, err
		}
		text := "Private credential requested. Wait for the user's response; do not invent a password."
		if ready {
			text = "Private credential is ready. Use typeSecret with the same reference."
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}, nil
	}
	if name == "chat_message" {
		var request struct {
			ReplyTo string   `json:"replyTo"`
			Contact string   `json:"contact"`
			Text    string   `json:"text"`
			Files   []string `json:"files"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Text) == "" || len(request.Text) > 100_000 {
			return nil, fmt.Errorf("provide response text")
		}
		if request.ReplyTo != "" {
			if err := validateTmuxToken("replyTo", request.ReplyTo); err != nil {
				return nil, err
			}
		}
		if request.Contact != "" {
			if err := validateContactRef(request.Contact); err != nil {
				return nil, err
			}
			if len(request.Files) > 0 {
				return nil, fmt.Errorf("contact messages cannot carry image files")
			}
			if err := requireContact(ctx, assignment, request.Contact); err != nil {
				return nil, err
			}
			if err := writeChatEvent(ctx, ChatEvent{Kind: "contact", Contact: request.Contact, Text: request.Text}); err != nil {
				return nil, err
			}
			return map[string]any{"content": []map[string]any{{"type": "text", "text": "Message delivered to the contact's conversation."}}}, nil
		}
		images, err := loadChatImages(request.Files)
		if err != nil {
			return nil, err
		}
		if err := writeChatEvent(ctx, ChatEvent{Kind: "reply", ReplyTo: request.ReplyTo, Text: request.Text, Images: images}); err != nil {
			return nil, err
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "Response delivered to vmbox Agent chat."}}}, nil
	}
	if name == "chat_ask" {
		var request struct {
			ReplyTo  string   `json:"replyTo"`
			Contact  string   `json:"contact"`
			Question string   `json:"question"`
			Choices  []string `json:"choices"`
			Multiple bool     `json:"multiple"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Question) == "" || len(request.Choices) < 1 || len(request.Choices) > 20 {
			return nil, fmt.Errorf("provide question and choices")
		}
		if request.ReplyTo != "" {
			if err := validateTmuxToken("replyTo", request.ReplyTo); err != nil {
				return nil, err
			}
		}
		for _, choice := range request.Choices {
			if strings.TrimSpace(choice) == "" || len(choice) > 500 {
				return nil, fmt.Errorf("invalid choice")
			}
		}
		if request.Contact != "" {
			if err := validateContactRef(request.Contact); err != nil {
				return nil, err
			}
			if err := requireContact(ctx, assignment, request.Contact); err != nil {
				return nil, err
			}
			text := request.Question + "\n\nChoices:"
			for _, choice := range request.Choices {
				text += "\n- " + choice
			}
			if request.Multiple {
				text += "\n\nOne or more choices may be selected."
			}
			if err := writeChatEvent(ctx, ChatEvent{Kind: "contact", Contact: request.Contact, Text: text}); err != nil {
				return nil, err
			}
			return map[string]any{"content": []map[string]any{{"type": "text", "text": "Question delivered to the contact's conversation."}}}, nil
		}
		event := ChatEvent{Kind: "question", ReplyTo: request.ReplyTo, Text: request.Question, Question: &ChatQuestion{Text: request.Question, Choices: request.Choices, Multiple: request.Multiple}}
		if err := writeChatEvent(ctx, event); err != nil {
			return nil, err
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "Question delivered to vmbox Agent chat."}}}, nil
	}
	if name == "typeSecret" {
		var key string
		if json.Unmarshal(values["key"], &key) != nil {
			return nil, fmt.Errorf("secret key must be a string")
		}
		if err := desktopSecretReference(ctx, assignment, key); err != nil {
			return nil, err
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "Password inserted. Form not submitted."}}}, nil
	}
	if name == "secret_ensure" {
		var key, purpose string
		if json.Unmarshal(values["key"], &key) != nil || json.Unmarshal(values["purpose"], &purpose) != nil || purpose != "new_account_password" {
			return nil, fmt.Errorf("provide a reference and new_account_password purpose")
		}
		var policy secrets.PasswordPolicy
		if json.Unmarshal(args, &policy) != nil {
			return nil, fmt.Errorf("invalid password policy")
		}
		if err := policy.Validate(); err != nil {
			return nil, err
		}
		created, err := desktopSecretOperation(ctx, assignment, key, true, policy)
		if err != nil {
			return nil, err
		}
		status := "Existing password reference retained."
		if created {
			status = "New password reference saved; pending use."
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": status + " Use typeSecret with the same key to fill it."}}}, nil
	}
	if name == "capture_window" {
		var identifier string
		if value, ok := values["window_id"]; ok {
			if string(value) == "null" || json.Unmarshal(value, &identifier) != nil {
				return nil, fmt.Errorf("window_id must be a string")
			}
			if _, err := windowID(identifier); err != nil {
				return nil, err
			}
		}
		var captured bytes.Buffer
		bounds, err := CaptureWindow(ctx, assignment, identifier, &captured)
		if err != nil {
			return nil, fmt.Errorf("window capture unavailable: %w", err)
		}
		return map[string]any{"content": []map[string]any{
			{"type": "text", "text": fmt.Sprintf("Desktop coordinates: x=%d, y=%d, width=%d, height=%d. Capture includes any overlapping windows.", bounds.Min.X, bounds.Min.Y, bounds.Dx(), bounds.Dy())},
			{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(captured.Bytes())},
		}}, nil
	}
	if name == "desktop_screenshot" {
		var png bytes.Buffer
		if err := CaptureDesktop(ctx, assignment, &png); err != nil {
			return nil, fmt.Errorf("desktop capture unavailable; check that this box's desktop is running")
		}
		return map[string]any{"content": []map[string]any{{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(png.Bytes())}}}, nil
	}
	var action DesktopAction
	if err := json.Unmarshal(args, &action); err != nil {
		return nil, fmt.Errorf("invalid desktop arguments")
	}
	action.Action = strings.TrimPrefix(name, "desktop_")
	if action.Action == "click" && action.Count > 2 {
		return nil, fmt.Errorf("click count must be 1 or 2")
	}
	if err := DesktopInput(ctx, assignment, action); err != nil {
		return nil, err
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": "Action completed. Capture the screen to inspect its result."}}}, nil
}
