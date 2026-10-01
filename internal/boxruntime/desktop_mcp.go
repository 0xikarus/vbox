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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/secrets"
)

type desktopMCPRequest struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

const maxDesktopMCPMessageCharacters = 2000

// Keep enough room for JSON escaping and attachment paths; oversized chat text
// receives a normal tool error instead of stopping the MCP scanner.
const maxDesktopMCPRequestBytes = 1 << 20

func validateDesktopMCPMessageLength(message string) error {
	if utf8.RuneCountInString(message) > maxDesktopMCPMessageCharacters {
		return fmt.Errorf("message exceeds %d characters; shorten or split it before retrying", maxDesktopMCPMessageCharacters)
	}
	return nil
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
		makeTool("get_contacts", "List the boxes this box is permitted to message. Returns a compact id, exact box name, chat group, agent, state and whether messaging is allowed. Groups are owner-organized labels and do not grant access. Use either the returned id or exact name in chat_message or chat_ask. The controller enforces this list; you cannot message a box that is not returned here.", map[string]any{}),
		makeTool("heartbeat", "Manage this box's local heartbeat. Use action=start with intervalMinutes (5–1440) and optional count (default 1) to schedule prompts; starting again replaces the timer. Use action=stop with no other arguments to stop it, even when the agent conversation has closed. A hibernated box cannot tick or wake itself; due ticks resume after an external wake. With count above 1, prompts include the ticks left after that prompt.", map[string]any{"action": map[string]any{"type": "string", "enum": []string{"start", "stop"}}, "intervalMinutes": map[string]any{"type": "integer", "minimum": 5, "maximum": 1440}, "count": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "default": 1}}, "action"),
		makeTool("get_run_budget", "Get this box's durable run-time budget. The countdown advances only while the box is allocated and is separate from desktop inactivity.", map[string]any{}),
		makeTool("get_thread_history", "Read a paginated direct or shared-chat thread this box already has access to. Pass chatId for a shared-chat thread. A thread reference alone never grants access.", map[string]any{"threadId": map[string]any{"type": "string", "minLength": 36, "maxLength": 36}, "chatId": map[string]any{"type": "string", "minLength": 36, "maxLength": 36}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "before": map[string]any{"type": "string"}, "beforeId": map[string]any{"type": "string", "minLength": 36, "maxLength": 36}}, "threadId"),
		makeTool("list_agent_boxes", "List safe lifecycle summaries for the account's agent boxes. Does not expose provider credentials, volume identifiers, terminal access, or desktop access.", map[string]any{}),
		makeTool("get_agent_box", "Inspect one agent box's safe lifecycle details by ID or exact name. Does not grant terminal or desktop access.", map[string]any{"box": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "box"),
		makeTool("get_agent_box_screenshot", "Capture another running, unprotected agent box's current desktop as a PNG image by ID or exact name. Does not wake a box, start its desktop, or grant desktop control. Use take_screenshot for this box's own desktop.", map[string]any{"box": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "thumbnail": map[string]any{"type": "boolean", "default": false}}, "box"),
		makeTool("create_agent_box", "Create an agent box in an available account worker pool within this box's agent, disk, and count permission limits. The creator and new box become mutual direct contacts automatically. Call get_agent_box_configs with mode=list for saved profiles, roles, and exact tool preset IDs; call get_available_workers for free worker slots across pools. Pass slotId to choose a specific slot and its pool, or omit it for automatic placement. Optional memoryGiB (1–8) and swapGiB (0–4) require a container-isolated shared-worker pool; omitted values use 2 GiB RAM and 1 GiB swap there. Pass tools as an array of preset IDs such as [\"blender\"] or [\"foundry\"], never as a string. Presets install before the new box becomes usable. loginProfiles imports one matching agent profile and optionally one GitHub profile; model and reasoningEffort override that saved profile. Optional instructions become managed startup instructions. Reuse idempotencyKey when retrying.", map[string]any{"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 100}, "agent": map[string]any{"type": "string", "enum": []string{"codex", "claude", "opencode"}}, "diskGiB": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000}, "memoryGiB": map[string]any{"type": "integer", "minimum": 1, "maximum": 8}, "swapGiB": map[string]any{"type": "integer", "minimum": 0, "maximum": 4}, "slotId": map[string]any{"type": "string", "minLength": 1, "description": "Exact slotId returned by get_available_workers; selects that slot and pool."}, "tools": map[string]any{"type": "array", "maxItems": 3, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": []string{"foundry", "blender", "desktop"}}}, "loginProfiles": map[string]any{"type": "array", "maxItems": 2, "items": map[string]any{"type": "object", "properties": map[string]any{"application": map[string]any{"type": "string", "enum": []string{"codex", "claude", "opencode", "github"}}, "name": map[string]any{"type": "string", "minLength": 1}, "model": map[string]any{"type": "string"}, "reasoningEffort": map[string]any{"type": "string"}}, "required": []string{"application", "name"}, "additionalProperties": false}}, "roleIds": map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string", "minLength": 1}}, "instructions": map[string]any{"type": "string", "maxLength": v1.MaxInstructionMarkdownBytes, "description": "Managed Markdown instructions given to the new agent at startup."}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "name", "agent", "idempotencyKey"),
		makeTool("get_agent_box_configs", "Use mode=list to read exact saved login profile references, assignable role IDs, tool preset IDs, allowed agents and creation limits. Use mode=models with application and name to read that saved Claude, Codex or OpenCode profile's live model catalog. Never returns credentials.", map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"list", "models"}}, "application": map[string]any{"type": "string", "enum": []string{"claude", "codex", "opencode"}}, "name": map[string]any{"type": "string", "minLength": 1}}, "mode"),
		makeTool("get_available_workers", "List healthy free worker slots across this account's configured pools. Returns slotId, provider, providerCredential, serviceName, ordinal, region, and memoryConfigurable for container-isolated pools. Pass a returned slotId to create_agent_box to choose that slot and pool. Availability is checked again when creating the box.", map[string]any{}),
		makeTool("set_agent_box_tags", "Replace an agent box's plain metadata tags. Tags are labels only and never grant contact or tool access.", map[string]any{"box": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "tags": map[string]any{"type": "array", "maxItems": 20, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 32}}}, "box", "tags"),
		makeTool("set_agent_box_run_budget", "Set another unprotected box's run-time budget and restart its current countdown. The countdown advances only while the box is running. Use seconds=0 to disable automatic hibernation from the run budget; otherwise choose 60–2592000 seconds (up to 30 days). This is separate from desktop inactivity. confirmation must exactly match the target box name.", map[string]any{"box": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 2592000}, "confirmation": map[string]any{"type": "string", "minLength": 1, "maxLength": 100}}, "box", "seconds", "confirmation"),
		makeTool("restart_agent_box", "Hibernate and start another running, unprotected agent box again. Running agents and terminal sessions end. confirmation must exactly match the target box name. Reuse idempotencyKey when retrying.", map[string]any{"box": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "confirmation": map[string]any{"type": "string", "minLength": 1, "maxLength": 100}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "box", "confirmation", "idempotencyKey"),
		makeTool("wake_agent_box", "Wake another hibernated, unprotected agent box without restarting a running box. Optionally set sessionChoice to restore the saved conversation or start fresh after wake. The request may queue until capacity is free; use get_agent_box to check progress if allowed. confirmation must exactly match the target box name. Reuse idempotencyKey when retrying.", map[string]any{"box": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "confirmation": map[string]any{"type": "string", "minLength": 1, "maxLength": 100}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "sessionChoice": map[string]any{"type": "string", "enum": []string{"restore", "fresh"}}}, "box", "confirmation", "idempotencyKey"),
		makeTool("clear_agent_box_context", "Start a fresh agent conversation in another running, unprotected box while keeping its chat history and workspace. confirmation must exactly match the target box name. Reuse idempotencyKey when retrying.", map[string]any{"box": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "confirmation": map[string]any{"type": "string", "minLength": 1, "maxLength": 100}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "box", "confirmation", "idempotencyKey"),
		makeTool("compact_agent_box_context", "Request /compact in another running, unprotected agent box's existing conversation. The target must be idle; this preserves its thread and workspace. The response confirms that compaction was requested, not that summarization has finished. confirmation must exactly match the target box name. Reuse idempotencyKey when retrying.", map[string]any{"box": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "confirmation": map[string]any{"type": "string", "minLength": 1, "maxLength": 100}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "box", "confirmation", "idempotencyKey"),
		makeTool("delete_agent_box", "Permanently delete another, unprotected agent box. confirmation must exactly match the target box name. Reuse idempotencyKey when retrying.", map[string]any{"box": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "confirmation": map[string]any{"type": "string", "minLength": 1, "maxLength": 100}, "idempotencyKey": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "box", "confirmation", "idempotencyKey"),
		makeTool("set_busy", "Report whether this agent is actively working. Submitted chat messages set busy automatically and chat_message/chat_ask clear it automatically; call this only to override activity outside that normal request/reply flow.", map[string]any{"busy": map[string]any{"type": "boolean"}}, "busy"),
		makeTool("chat_message", "Send a message of at most 2000 characters to the vbox Agent chat. For the account owner, pass text and optionally replyTo; OMIT contact entirely. replyTo is the chat message reference, never a box contact. Call this once for each completed response, including any image files the user should receive. To send to another box, pass contact as a compact id or exact box name returned by get_contacts. To reply to an incoming contact message, pass its From-Box-ID as contact and omit replyTo. Image files are supported for both owner and contact messages.", map[string]any{"text": map[string]any{"type": "string", "minLength": 1, "maxLength": maxDesktopMCPMessageCharacters}, "replyTo": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "contact": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "files": map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string"}}}, "text"),
		makeTool("chat_ask", "Ask the user to choose one or more options in vbox Agent chat when their decision is required. The question and all choices together must fit within 2000 characters. replyTo is optional; without it the question is delivered on its own. Pass a compact id or exact box name returned by get_contacts to ask another box's agent instead of the owner.", map[string]any{"question": map[string]any{"type": "string", "minLength": 1, "maxLength": maxDesktopMCPMessageCharacters}, "choices": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 500}}, "multiple": map[string]any{"type": "boolean"}, "replyTo": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "contact": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "question", "choices"),
		makeTool("secret_request", "Request an existing account password privately from the user for the focused HTTPS password field. Never generate a substitute. Call again to check readiness.", map[string]any{"key": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}}, "key"),
		makeTool("generate_password", "Generate and securely store a password for a new account on the focused HTTPS password field's origin. Never use this for an existing account's credential.", map[string]any{"length": map[string]any{"type": "integer", "minimum": 16, "maximum": 128}, "alphabet": map[string]any{"type": "string", "minLength": 32, "maxLength": 94}, "key": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}, "purpose": map[string]any{"type": "string", "enum": []string{"new_account_password"}}}, "key", "purpose"),
		makeTool("type_secret", "Fill the focused password field using an existing secret reference. Does not reveal the password, generate a new one, or submit the form.", map[string]any{"key": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}}, "key"),
		makeTool("take_screenshot", "Capture this box's current desktop as a PNG image. Use output=file for a private PNG path that can be passed to chat_message files. Does not start or wake the desktop.", map[string]any{"output": map[string]any{"type": "string", "enum": []string{"image", "file"}, "default": "image"}}),
		makeTool("capture_window", "Capture the visible screen area of an X11 window as PNG. Defaults to the active window; optionally supply window_id (decimal or 0x hexadecimal). Does not focus or raise windows. Overlapping windows appear in the capture; minimized windows are not supported. Returned x/y offsets map image coordinates to desktop coordinates.", map[string]any{"window_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 10}}),
		makeTool("move_mouse", "Move the cursor smoothly to a screen coordinate.", point, "x", "y"),
		makeTool("click_mouse", "Move to a coordinate and click. Button: 1 left, 2 middle, 3 right. Count 2 sends a double-click with a brief inter-click delay.", map[string]any{"x": integer, "y": integer, "button": map[string]any{"type": "integer", "minimum": 1, "maximum": 3}, "count": map[string]any{"type": "integer", "minimum": 1, "maximum": 2}}, "x", "y"),
		makeTool("drag_mouse", "Drag directly from x/y to toX/toY while holding the left button.", map[string]any{"x": integer, "y": integer, "toX": integer, "toY": integer}, "x", "y", "toX", "toY"),
		makeTool("scroll_mouse", "Scroll at a coordinate. Text is direction; count is 1–20 wheel steps.", map[string]any{"x": integer, "y": integer, "text": map[string]any{"type": "string", "enum": []string{"up", "down", "left", "right"}}, "count": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}}, "x", "y", "text"),
		makeTool("type_text", "Type ordinary literal text in the focused application. Use the secret service for credentials.", map[string]any{"text": map[string]any{"type": "string", "minLength": 1, "maxLength": 16384}}, "text"),
		makeTool("press_keys", "Press a shortcut: keys contains modifiers and a key, e.g. [ctrl,l] or [Return].", map[string]any{"keys": map[string]any{"type": "array", "minItems": 1, "maxItems": 5, "items": map[string]any{"type": "string"}}}, "keys"),
	}
}

func desktopContactLine(contact ContactSummary) string {
	state := contact.State
	if state == "" {
		state = "unknown"
	}
	group := "none"
	if contact.Group != "" {
		group = strconv.Quote(contact.Group)
	}
	return fmt.Sprintf("- id %s | name %s | group %s | agent %s | %s | message %t", contact.ID, contact.Name, group, contact.Agent, state, contact.CanMessage)
}

type desktopToolPolicyResolver func(context.Context, string) (map[string]bool, error)

var desktopToolPolicyPollInterval = 2 * time.Second

func allDesktopToolPolicy(_ context.Context, _ string) (map[string]bool, error) {
	allowed := map[string]bool{}
	for _, tool := range desktopMCPTools() {
		allowed[tool["name"].(string)] = true
	}
	return allowed, nil
}

func allowedDesktopMCPTools(ctx context.Context, assignment string, resolve desktopToolPolicyResolver) ([]map[string]any, map[string]bool, error) {
	allowed, err := resolve(ctx, assignment)
	if err != nil {
		return nil, nil, err
	}
	tools := []map[string]any{}
	filtered := map[string]bool{}
	for _, tool := range desktopMCPTools() {
		name := tool["name"].(string)
		if allowed[name] {
			tools = append(tools, tool)
			filtered[name] = true
		}
	}
	return tools, filtered, nil
}

const desktopMCPGuidePath = ".config/vmbox/mcp-tools.md"

// Persist first, then push text events directly to the controller. Image events
// and failed callbacks still use the durable outbox pull path.
func writeDesktopChatEvent(ctx context.Context, assignment string, event ChatEvent) error {
	session, err := chatSession(ctx)
	if err != nil {
		return err
	}
	if event.ID == "" {
		event.ID, err = chatEventID()
		if err != nil {
			return err
		}
	}
	if err := writeChatEvent(ctx, event); err != nil {
		return err
	}
	go func() {
		requestCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		notifyDesktopChatReady(requestCtx, assignment, os.Getenv("HOME"), session, event, desktopAgentAPI)
	}()
	return nil
}

// Contact text needs a delivery verdict in the tool response. Persist the
// event first so a lost controller response cannot lose the message; the
// controller deduplicates retries by event ID. A network error is explicitly
// unconfirmed, while a controller rejection can be reported to the agent.
func sendDesktopContactEvent(ctx context.Context, assignment string, event ChatEvent) (string, error) {
	if len(event.Images) != 0 {
		if err := writeDesktopChatEvent(ctx, assignment, event); err != nil {
			return "", err
		}
		return "Contact message queued with images; delivery is not yet confirmed. Check the conversation before retrying.", nil
	}
	session, err := chatSession(ctx)
	if err != nil {
		return "", err
	}
	event.ID, err = chatEventID()
	if err != nil {
		return "", err
	}
	if err := writeChatEvent(ctx, event); err != nil {
		return "", err
	}
	var result struct {
		Stored    bool   `json:"stored"`
		Delivered bool   `json:"delivered"`
		Reason    string `json:"reason"`
	}
	err = desktopAgentAPI(ctx, assignment, http.MethodPost, "/v1/agent-desktop/chat-ready", map[string]any{"session": session, "event": event}, &result)
	if err != nil || !result.Stored {
		return "Contact message queued locally. Delivery is unconfirmed and will retry automatically while this box is running.", nil
	}
	if err := AckChatEvent(os.Getenv("HOME"), session, event.ID); err != nil {
		return "Contact delivery recorded. Local confirmation is pending; retries use the same message ID.", nil
	}
	if !result.Delivered {
		return "", fmt.Errorf("contact message rejected: %s", result.Reason)
	}
	return "Message delivered to the contact's conversation.", nil
}

func notifyDesktopChatReady(ctx context.Context, assignment, home, session string, event ChatEvent, post func(context.Context, string, string, string, any, any) error) {
	const path = "/v1/agent-desktop/chat-ready"
	if len(event.Images) == 0 {
		var result struct {
			Stored bool `json:"stored"`
		}
		if err := post(ctx, assignment, http.MethodPost, path, map[string]any{"session": session, "event": event}, &result); err == nil && result.Stored {
			_ = AckChatEvent(home, session, event.ID)
			return
		}
	}
	// The original hint remains compatible with older controllers and lets the
	// controller pull the file when direct delivery could not be confirmed.
	fallbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = post(fallbackCtx, assignment, http.MethodPost, path, map[string]string{"session": session}, nil)
}

// writeDesktopMCPGuide gives every managed agent a local, readable tool
// reference. It is generated from the same schemas advertised over MCP.
func writeDesktopMCPGuide(home string) error {
	var guide strings.Builder
	guide.WriteString("# vmbox-desktop MCP tools\n\n")
	guide.WriteString("Call tools by the exact snake_case name below and pass one JSON object matching its schema. Do not invent language-style pseudo calls.\n\n")
	guide.WriteString("## Contacting other boxes\n\n")
	guide.WriteString("Each result has a compact `id` and exact box `name`; either is accepted. Discover new contacts first and never guess an internal UUID. An incoming contact message includes a full `From-Box-ID` that is accepted for replies while that box remains an authorized contact.\n\n")
	guide.WriteString("1. Discover allowed contacts: `get_contacts {}`\n")
	guide.WriteString("2. Send by exact name: `chat_message {\"contact\":\"reviewer\",\"text\":\"Please review commit abc123.\"}`\n")
	guide.WriteString("   The same call may use the compact ID: `chat_message {\"contact\":\"a1b2c3d4\",\"text\":\"Please review commit abc123.\"}`\n")
	guide.WriteString("3. Ask a choice: `chat_ask {\"contact\":\"planner\",\"question\":\"Which option should we ship?\",\"choices\":[\"A\",\"B\"],\"multiple\":false}`\n")
	guide.WriteString("4. Reply to an incoming box message using its `From-Box-ID`: `chat_message {\"contact\":\"a1b2c3d4-1234-4000-8000-000000000000\",\"text\":\"Applied your feedback.\"}`. Omit `replyTo`.\n\n")
	guide.WriteString("Only contacts returned by `get_contacts` are permitted. If a box is absent, ask the owner to add it as a direct contact or grant All contacts. Omit `contact` to message the owner. Add `files` with absolute PNG, JPEG, or GIF paths to attach images to either kind of message.\n\n")
	guide.WriteString("## Send a prompt from a box-local app\n\n")
	guide.WriteString("Read `~/.local/share/vmbox/mcp-http.json` inside the box. Its `promptUrl` is a local `http://127.0.0.1:<port>/prompt` address and its `token` authorizes the request. POST one JSON object with non-empty `text`, for example `{\"text\":\"Check the latest build result\"}`, and send `Authorization: Bearer <token>` and `Content-Type: application/json` headers. The token also authorizes HTTP MCP tools, so keep it private.\n\n")
	guide.WriteString("A `202` response contains `accepted`, `session`, and `messageId`; the agent's reply goes through its normal conversation, not the HTTP response. `/prompt` requires a running managed agent and never starts or wakes one. If several conversations are running, include `session` in the JSON body or use an `X-Vmbox-Session` header. For a retryable local job, set one stable `messageId` in the JSON body and reuse it on retries; a `409` may still mean delivery is uncertain.\n\n")
	for _, tool := range desktopMCPTools() {
		name := tool["name"].(string)
		description := tool["description"].(string)
		schema, err := json.Marshal(tool["inputSchema"])
		if err != nil {
			return err
		}
		fmt.Fprintf(&guide, "## %s\n\n%s\n\nSchema: `%s`\n\n", name, description, schema)
	}
	return writeTextAtomic(filepath.Join(home, desktopMCPGuidePath), guide.String(), 0600)
}

// ServeDesktopMCP is a local stdio adapter. stdout contains protocol only; the
// assignment captured at startup fences every subsequent operation.
func ServeDesktopMCP(ctx context.Context, assignment string, input io.Reader, output io.Writer) error {
	if home, err := os.UserHomeDir(); err == nil {
		_ = writeDesktopMCPGuide(home)
	}
	return serveDesktopMCP(ctx, assignment, input, output, desktopAgentToolPolicy)
}

func serveDesktopMCP(ctx context.Context, assignment string, input io.Reader, output io.Writer, resolve desktopToolPolicyResolver) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), maxDesktopMCPRequestBytes)
	encoder := json.NewEncoder(output)
	var outputMu sync.Mutex
	encode := func(value any) error {
		outputMu.Lock()
		defer outputMu.Unlock()
		return encoder.Encode(value)
	}
	var channelOnce sync.Once
	var policyWatchOnce sync.Once
	var claudeChannelClient bool
	var mascotAgent string
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	for scanner.Scan() {
		var request desktopMCPRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if err = encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "Invalid JSON"}}); err != nil {
				return err
			}
			continue
		}
		if len(request.ID) == 0 {
			if request.Method == "notifications/initialized" {
				policyWatchOnce.Do(func() { go watchDesktopToolPolicy(ctx, assignment, resolve, encode) })
				if mascotAgent != "" {
					go runMascotHeartbeat(heartbeatCtx, assignment, mascotAgent)
					mascotAgent = ""
				}
				if claudeChannelClient {
					// The client only registers notification handlers after the
					// initialize response. Waiting for its initialized notification
					// prevents a queued first message from being emitted too early.
					channelOnce.Do(func() { go serveClaudeChannel(ctx, encode) })
				}
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
			mascotAgent = mascotClientAgent(params.ClientInfo.Name)
			version := params.ProtocolVersion
			if version != "2024-11-05" && version != "2025-03-26" && version != "2025-06-18" && version != "2025-11-25" {
				version = "2025-06-18"
			}
			response["result"] = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{"listChanged": true}, "experimental": map[string]any{"claude/channel": map[string]any{}}}, "serverInfo": map[string]any{"name": "vmbox-desktop", "version": "0.2.0"}, "instructions": "Messages from vbox Agent chat arrive as channel messages. Call exact snake_case MCP tool names with a JSON object: chat_message {\"replyTo\":\"message-ref\",\"text\":\"...\"} for replies and chat_ask for choices. To contact another box, call get_contacts {}, then chat_message {\"contact\":\"reviewer\",\"text\":\"...\"} using either its returned compact id or exact name. To reply to an incoming contact message, use its From-Box-ID as contact and omit replyTo. The tool list automatically refreshes when this box's owner changes its permissions. Busy state is automatic for normal replies; use set_busy only for other work. Incoming chat images arrive with an image_path channel attribute; read that path. Read ~/.config/vmbox/mcp-tools.md for every exact call and example. HTTP tools: read ~/.local/share/vmbox/mcp-http.json, then POST JSON to {url}/tools/{name} with its Bearer token. A script can POST {\"text\":\"...\"} to promptUrl to deliver a user message to this already-running agent conversation; it never wakes a stopped box."}
		case "ping":
			response["result"] = map[string]any{}
		case "tools/list":
			tools, _, err := allowedDesktopMCPTools(ctx, assignment, resolve)
			if err != nil {
				response["error"] = map[string]any{"code": -32000, "message": "MCP tool policy unavailable"}
				break
			}
			response["result"] = map[string]any{"tools": tools}
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				response["error"] = map[string]any{"code": -32602, "message": "Invalid tool parameters"}
				break
			}
			callCtx, cancel := context.WithTimeout(ctx, desktopToolTimeout(params.Name))
			_, allowed, policyErr := allowedDesktopMCPTools(callCtx, assignment, resolve)
			var result map[string]any
			var err error
			if policyErr != nil {
				err = fmt.Errorf("MCP tool policy unavailable")
			} else if !allowed[params.Name] {
				err = fmt.Errorf("MCP tool %s is not allowed for this box", params.Name)
			} else {
				result, err = callDesktopTool(callCtx, assignment, params.Name, params.Arguments)
			}
			_ = queueLocalMCPActivity(assignment, params.Name, params.Arguments, err)
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

func desktopToolTimeout(name string) time.Duration {
	if name == "compact_agent_box_context" {
		return 50 * time.Second
	}
	return 30 * time.Second
}

func desktopToolPolicySignature(ctx context.Context, assignment string, resolve desktopToolPolicyResolver) (string, error) {
	_, allowed, err := allowedDesktopMCPTools(ctx, assignment, resolve)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(allowed))
	for name, enabled := range allowed {
		if enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, "\n"), nil
}

// watchDesktopToolPolicy makes permission edits visible to a running MCP
// client without restarting the agent. Calls are still authorized separately,
// so revocations take effect even before a client refreshes its cached list.
func watchDesktopToolPolicy(ctx context.Context, assignment string, resolve desktopToolPolicyResolver, encode func(any) error) {
	previous, err := desktopToolPolicySignature(ctx, assignment, resolve)
	if err != nil {
		previous = ""
	}
	ticker := time.NewTicker(desktopToolPolicyPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, err := desktopToolPolicySignature(ctx, assignment, resolve)
			if err != nil || current == previous {
				continue
			}
			previous = current
			if encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"}) != nil {
				return
			}
		}
	}
}

var claudeChannelPollInterval = 500 * time.Millisecond

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
	claudeConversationID := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if !claudeSessionID.MatchString(claudeConversationID) {
		claudeConversationID = ""
	}
	ready := claudeChannelReadyPath(home, session, owner)
	defer os.Remove(ready)
	ticker := time.NewTicker(claudeChannelPollInterval)
	defer ticker.Stop()
	// A successful write to this live Claude channel can remain queued in the
	// client until its current turn finishes. Its transcript receipt may therefore
	// appear much later. Emit each inbox event once per MCP connection; a new
	// connection after a TUI restart gets a fresh map and can retry it.
	sent := map[string]bool{}
	for {
		if !claudeChannelCurrent(ctx, session) {
			// A brief tmux/worker interruption does not close Claude's MCP
			// connection. Keep polling so this same connection can resume
			// delivery when the pane becomes reachable again.
			_ = os.Remove(ready)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				continue
			}
		}
		marker, _ := json.Marshal(map[string]any{"owner": owner, "pid": os.Getpid(), "sessionId": claudeConversationID})
		if writeTextAtomic(ready, string(marker), 0600) != nil {
			// A temporary workspace I/O failure must not permanently detach
			// a still-running Claude MCP connection from Chat.
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				continue
			}
		}
		if events, err := pendingChatInbound(home, session); err == nil {
			emitted := false
			for _, pending := range events {
				event, path := pending.event, pending.path
				accepted, receiptErr := claudeNativeReceipt(ctx, home, session, event.ID, pending.mtime)
				if receiptErr == nil && accepted {
					_ = ackClaudeNativeReceipt(home, session, event.ID, path)
					delete(sent, event.ID)
				} else if receiptErr == nil && !emitted {
					emitted = emitClaudeChannelEvent(session, event, sent, encode)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func emitClaudeChannelEvent(session string, event chatInboundFile, sent map[string]bool, encode func(any) error) bool {
	if sent[event.ID] {
		return false
	}
	meta := map[string]string{"chat_id": session, "message_id": event.ID, "user": "vmbox-user", "ts": time.Now().UTC().Format(time.RFC3339Nano)}
	if len(event.Paths) > 0 {
		meta["image_path"] = event.Paths[0]
		paths, _ := json.Marshal(event.Paths)
		meta["image_paths"] = string(paths)
	}
	if err := encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/claude/channel", "params": map[string]any{"content": event.Text, "meta": meta}}); err != nil {
		return false
	}
	sent[event.ID] = true
	return true
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
			path := filepath.Join(claudeChannelReadyDir(home), entry.Name())
			data, err := os.ReadFile(path)
			var marker struct {
				Owner string `json:"owner"`
				PID   int    `json:"pid"`
			}
			if err == nil && json.Unmarshal(data, &marker) == nil && marker.Owner == strings.TrimPrefix(entry.Name(), session+".") && claudeChannelOwnerAlive(marker.PID) {
				owners[marker.Owner] = struct{}{}
			} else {
				_ = os.Remove(path)
			}
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
	if name == "heartbeat" {
		var request struct {
			Action          string `json:"action"`
			IntervalMinutes int    `json:"intervalMinutes"`
			Count           int    `json:"count"`
		}
		if json.Unmarshal(args, &request) != nil {
			return nil, fmt.Errorf("invalid heartbeat arguments")
		}
		if request.Action == "stop" {
			if _, ok := values["intervalMinutes"]; ok {
				return nil, fmt.Errorf("stop does not accept intervalMinutes or count")
			}
			if _, ok := values["count"]; ok {
				return nil, fmt.Errorf("stop does not accept intervalMinutes or count")
			}
			result, err := stopLocalHeartbeat()
			if err != nil {
				return nil, err
			}
			return desktopToolJSON(result)
		}
		if request.Action != "start" || request.IntervalMinutes < 5 || request.IntervalMinutes > 1440 || request.Count < 0 || request.Count > 1000 {
			return nil, fmt.Errorf("intervalMinutes must be 5–1440 and count 1–1000")
		}
		if request.Count == 0 {
			request.Count = 1
		}
		session, err := chatSession(ctx)
		if err != nil {
			return nil, err
		}
		result, err := startLocalHeartbeat(session, request.IntervalMinutes, request.Count)
		if err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
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
			lines = append(lines, desktopContactLine(contact))
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
	if name == "create_agent_box" {
		var request struct {
			Name           string               `json:"name"`
			Agent          string               `json:"agent"`
			DiskGiB        int                  `json:"diskGiB"`
			MemoryGiB      int                  `json:"memoryGiB"`
			SwapGiB        *int                 `json:"swapGiB"`
			SlotID         string               `json:"slotId"`
			Tools          []string             `json:"tools"`
			LoginProfiles  []v1.LoginProfileRef `json:"loginProfiles"`
			RoleIDs        []string             `json:"roleIds"`
			Instructions   string               `json:"instructions"`
			IdempotencyKey string               `json:"idempotencyKey"`
		}
		if json.Unmarshal(args, &request) != nil || request.Name == "" || request.Agent == "" || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("name, agent, and idempotencyKey are required")
		}
		var result map[string]any
		if err := desktopAgentAPIWithKey(ctx, assignment, http.MethodPost, "/v1/agent-desktop/boxes", request.IdempotencyKey, map[string]any{"name": request.Name, "agent": request.Agent, "diskGiB": request.DiskGiB, "memoryGiB": request.MemoryGiB, "swapGiB": request.SwapGiB, "slotId": request.SlotID, "tools": request.Tools, "loginProfiles": request.LoginProfiles, "roleIds": request.RoleIDs, "instructions": request.Instructions}, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "get_agent_box_configs" {
		path, err := agentBoxConfigPath(args)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		if err := desktopAgentAPI(ctx, assignment, http.MethodGet, path, nil, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "get_available_workers" {
		var result []map[string]any
		if err := desktopAgentAPI(ctx, assignment, http.MethodGet, "/v1/agent-desktop/available-workers", nil, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "list_agent_boxes" {
		var result []map[string]any
		if err := desktopAgentAPI(ctx, assignment, http.MethodGet, "/v1/agent-desktop/boxes", nil, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "get_agent_box" {
		var request struct {
			Box string `json:"box"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Box) == "" {
			return nil, fmt.Errorf("box is required")
		}
		var result map[string]any
		if err := desktopAgentAPI(ctx, assignment, http.MethodGet, "/v1/agent-desktop/boxes/"+url.PathEscape(request.Box), nil, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "get_agent_box_screenshot" {
		var request struct {
			Box       string `json:"box"`
			Thumbnail bool   `json:"thumbnail"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Box) == "" {
			return nil, fmt.Errorf("box is required")
		}
		path := "/v1/agent-desktop/boxes/" + url.PathEscape(request.Box) + "/screenshot"
		if request.Thumbnail {
			path += "?thumbnail=true"
		}
		pixels, err := desktopAgentScreenshot(ctx, assignment, path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"content": []map[string]any{{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(pixels)}}}, nil
	}
	if name == "set_agent_box_tags" {
		var request struct {
			Box  string   `json:"box"`
			Tags []string `json:"tags"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Box) == "" || request.Tags == nil {
			return nil, fmt.Errorf("box and tags are required")
		}
		var result map[string]any
		if err := desktopAgentAPI(ctx, assignment, http.MethodPut, "/v1/agent-desktop/boxes/"+url.PathEscape(request.Box)+"/tags", map[string]any{"tags": request.Tags}, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "set_agent_box_run_budget" {
		var request struct {
			Box          string `json:"box"`
			Confirmation string `json:"confirmation"`
			Seconds      *int64 `json:"seconds"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Box) == "" || request.Confirmation == "" || request.Seconds == nil || *request.Seconds < 0 || *request.Seconds > 2592000 || (*request.Seconds > 0 && *request.Seconds < 60) {
			return nil, fmt.Errorf("box, exact name confirmation, and seconds (0 or 60–2592000) are required")
		}
		var result map[string]any
		if err := desktopAgentAPI(ctx, assignment, http.MethodPut, "/v1/agent-desktop/boxes/"+url.PathEscape(request.Box)+"/run-budget", map[string]any{"confirmation": request.Confirmation, "seconds": *request.Seconds}, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "delete_agent_box" {
		var request struct {
			Box            string `json:"box"`
			Confirmation   string `json:"confirmation"`
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Box) == "" || request.Confirmation == "" || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("box, confirmation, and idempotencyKey are required")
		}
		var result map[string]any
		if err := desktopAgentAPIWithKey(ctx, assignment, http.MethodDelete, "/v1/agent-desktop/boxes/"+url.PathEscape(request.Box), request.IdempotencyKey, map[string]any{"confirmation": request.Confirmation}, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "restart_agent_box" {
		var request struct {
			Box            string `json:"box"`
			Confirmation   string `json:"confirmation"`
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Box) == "" || request.Confirmation == "" || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("box, confirmation, and idempotencyKey are required")
		}
		var result map[string]any
		if err := desktopAgentAPIWithKey(ctx, assignment, http.MethodPost, "/v1/agent-desktop/boxes/"+url.PathEscape(request.Box)+"/restart", request.IdempotencyKey, map[string]any{"confirmation": request.Confirmation}, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "wake_agent_box" {
		var request struct {
			Box            string `json:"box"`
			Confirmation   string `json:"confirmation"`
			IdempotencyKey string `json:"idempotencyKey"`
			SessionChoice  string `json:"sessionChoice"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Box) == "" || request.Confirmation == "" || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("box, confirmation, and idempotencyKey are required")
		}
		if request.SessionChoice != "" && request.SessionChoice != "restore" && request.SessionChoice != "fresh" {
			return nil, fmt.Errorf("sessionChoice must be restore or fresh")
		}
		var result map[string]any
		if err := desktopAgentAPIWithKey(ctx, assignment, http.MethodPost, "/v1/agent-desktop/boxes/"+url.PathEscape(request.Box)+"/wake", request.IdempotencyKey, map[string]any{"confirmation": request.Confirmation, "sessionChoice": request.SessionChoice}, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "clear_agent_box_context" {
		var request struct {
			Box            string `json:"box"`
			Confirmation   string `json:"confirmation"`
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Box) == "" || request.Confirmation == "" || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("box, confirmation, and idempotencyKey are required")
		}
		var result map[string]any
		if err := desktopAgentAPIWithKey(ctx, assignment, http.MethodPost, "/v1/agent-desktop/boxes/"+url.PathEscape(request.Box)+"/clear-context", request.IdempotencyKey, map[string]any{"confirmation": request.Confirmation}, &result); err != nil {
			return nil, err
		}
		return desktopToolJSON(result)
	}
	if name == "compact_agent_box_context" {
		var request struct {
			Box            string `json:"box"`
			Confirmation   string `json:"confirmation"`
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Box) == "" || request.Confirmation == "" || request.IdempotencyKey == "" {
			return nil, fmt.Errorf("box, confirmation, and idempotencyKey are required")
		}
		var result map[string]any
		if err := desktopAgentAPIWithTimeout(ctx, assignment, http.MethodPost, "/v1/agent-desktop/boxes/"+url.PathEscape(request.Box)+"/compact", request.IdempotencyKey, map[string]any{"confirmation": request.Confirmation}, &result, 45*time.Second); err != nil {
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
			text = "Private credential is ready. Use type_secret with the same reference."
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
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Text) == "" {
			return nil, fmt.Errorf("provide response text")
		}
		if err := validateDesktopMCPMessageLength(request.Text); err != nil {
			return nil, err
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
			images, err := loadChatImages(request.Files)
			if err != nil {
				return nil, err
			}
			contact, err := resolveContact(ctx, assignment, request.Contact)
			if err != nil {
				return nil, err
			}
			confirmation, err := sendDesktopContactEvent(ctx, assignment, ChatEvent{Kind: "contact", ReplyTo: request.ReplyTo, Contact: contact, Text: request.Text, Images: images})
			if err != nil {
				return nil, err
			}
			return map[string]any{"content": []map[string]any{{"type": "text", "text": confirmation}}}, nil
		}
		images, err := loadChatImages(request.Files)
		if err != nil {
			return nil, err
		}
		if err := writeDesktopChatEvent(ctx, assignment, ChatEvent{Kind: "reply", ReplyTo: request.ReplyTo, Text: request.Text, Images: images}); err != nil {
			return nil, err
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "Response delivered to vbox Agent chat."}}}, nil
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
		questionLength := utf8.RuneCountInString(request.Question)
		for _, choice := range request.Choices {
			questionLength += utf8.RuneCountInString(choice)
		}
		if questionLength > maxDesktopMCPMessageCharacters {
			return nil, fmt.Errorf("message exceeds %d characters; shorten the question or choices before retrying", maxDesktopMCPMessageCharacters)
		}
		if request.Contact != "" {
			text := request.Question + "\n\nChoices:"
			for _, choice := range request.Choices {
				text += "\n- " + choice
			}
			if request.Multiple {
				text += "\n\nOne or more choices may be selected."
			}
			if err := validateDesktopMCPMessageLength(text); err != nil {
				return nil, err
			}
			if err := validateContactRef(request.Contact); err != nil {
				return nil, err
			}
			contact, err := resolveContact(ctx, assignment, request.Contact)
			if err != nil {
				return nil, err
			}
			confirmation, err := sendDesktopContactEvent(ctx, assignment, ChatEvent{Kind: "contact", Contact: contact, Text: text})
			if err != nil {
				return nil, err
			}
			return map[string]any{"content": []map[string]any{{"type": "text", "text": confirmation}}}, nil
		}
		event := ChatEvent{Kind: "question", ReplyTo: request.ReplyTo, Text: request.Question, Question: &ChatQuestion{Text: request.Question, Choices: request.Choices, Multiple: request.Multiple}}
		if err := writeDesktopChatEvent(ctx, assignment, event); err != nil {
			return nil, err
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "Question delivered to vbox Agent chat."}}}, nil
	}
	if name == "type_secret" {
		var key string
		if json.Unmarshal(values["key"], &key) != nil {
			return nil, fmt.Errorf("secret key must be a string")
		}
		if err := desktopSecretReference(ctx, assignment, key); err != nil {
			return nil, err
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "Password inserted. Form not submitted."}}}, nil
	}
	if name == "generate_password" {
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
		return map[string]any{"content": []map[string]any{{"type": "text", "text": status + " Use type_secret with the same key to fill it."}}}, nil
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
	if name == "take_screenshot" {
		output := "image"
		if value, ok := values["output"]; ok {
			if json.Unmarshal(value, &output) != nil || (output != "image" && output != "file") {
				return nil, fmt.Errorf("output must be image or file")
			}
		}
		var png bytes.Buffer
		if err := CaptureDesktop(ctx, assignment, &png); err != nil {
			return nil, fmt.Errorf("desktop capture unavailable; check that this box's desktop is running")
		}
		if output == "file" {
			path, err := saveDesktopScreenshot(png.Bytes())
			if err != nil {
				return nil, fmt.Errorf("save desktop capture: %w", err)
			}
			return map[string]any{"content": []map[string]any{{"type": "text", "text": "Screenshot saved to " + path + ". Pass this absolute path to chat_message files when the user should receive it."}}}, nil
		}
		return map[string]any{"content": []map[string]any{{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(png.Bytes())}}}, nil
	}
	var action DesktopAction
	if err := json.Unmarshal(args, &action); err != nil {
		return nil, fmt.Errorf("invalid desktop arguments")
	}
	action.Action = map[string]string{
		"move_mouse":   "move",
		"click_mouse":  "click",
		"drag_mouse":   "drag",
		"scroll_mouse": "scroll",
		"type_text":    "type",
		"press_keys":   "key",
	}[name]
	if action.Action == "click" && action.Count > 2 {
		return nil, fmt.Errorf("click count must be 1 or 2")
	}
	if err := DesktopInput(ctx, assignment, action); err != nil {
		return nil, err
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": "Action completed. Capture the screen to inspect its result."}}}, nil
}

func agentBoxConfigPath(args json.RawMessage) (string, error) {
	var request struct {
		Mode        string `json:"mode"`
		Application string `json:"application"`
		Name        string `json:"name"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return "", fmt.Errorf("invalid box config request")
	}
	switch request.Mode {
	case "list":
		if request.Application != "" || request.Name != "" {
			return "", fmt.Errorf("mode=list takes no application or name")
		}
		return "/v1/agent-desktop/box-configs", nil
	case "models":
		if (request.Application != "claude" && request.Application != "codex" && request.Application != "opencode") || strings.TrimSpace(request.Name) == "" {
			return "", fmt.Errorf("mode=models requires a Claude, Codex or OpenCode application and exact profile name")
		}
		return "/v1/agent-desktop/box-configs/" + url.PathEscape(request.Application) + "/" + url.PathEscape(request.Name) + "/models", nil
	default:
		return "", fmt.Errorf("mode must be list or models")
	}
}

func saveDesktopScreenshot(data []byte) (string, error) {
	dir := filepath.Join(WorkspaceRoot(), "tmp", "vmbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "desktop-screenshot.png")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return "", err
	}
	return path, nil
}
