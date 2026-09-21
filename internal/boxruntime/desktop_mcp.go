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
	"os"
	"path/filepath"
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

// A chat_message accepts 100,000 bytes of text. JSON escaping can expand that
// substantially, so the stdio frame limit must be larger than the tool's text
// limit or valid long replies make Scanner stop without a protocol response.
const maxDesktopMCPRequestBytes = 1 << 20

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
		makeTool("get_contacts", `List boxes this box may message. Call get_contacts({}); use a returned id/name in chat_message or chat_ask.`, map[string]any{}),
		makeTool("set_busy", `Set busy state only for work outside normal chat replies. Call set_busy({"busy":true}) or set_busy({"busy":false}).`, map[string]any{"busy": map[string]any{"type": "boolean"}}, "busy"),
		makeTool("chat_message", `Send one completed reply. Call chat_message({text:"...",replyTo:"...",files:["/path.png"]}). Use contact:"..." for another box; contact messages cannot include files.`, map[string]any{"text": map[string]any{"type": "string", "minLength": 1, "maxLength": 100000}, "replyTo": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "contact": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "files": map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string"}}}, "text"),
		makeTool("chat_ask", `Ask the user to choose. Call chat_ask({question:"...",choices:["A","B"]}); set multiple:true for multi-select.`, map[string]any{"question": map[string]any{"type": "string", "minLength": 1, "maxLength": 4000}, "choices": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 500}}, "multiple": map[string]any{"type": "boolean"}, "replyTo": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "contact": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "question", "choices"),
		makeTool("secret_request", `Request an existing password for the focused HTTPS field. Call secret_request({key:"..."}); wait, then call again. Never invent one.`, map[string]any{"key": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}}, "key"),
		makeTool("secret_ensure", `Create/reuse a password for a new account. Call secret_ensure({key:"...",purpose:"new_account_password"}); never use for an existing account.`, map[string]any{"length": map[string]any{"type": "integer", "minimum": 16, "maximum": 128}, "alphabet": map[string]any{"type": "string", "minLength": 32, "maxLength": 94}, "key": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}, "purpose": map[string]any{"type": "string", "enum": []string{"new_account_password"}}}, "key", "purpose"),
		makeTool("type_secret", `Fill the focused password field. Call type_secret({"key":"..."}); it does not reveal or submit the password.`, map[string]any{"key": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}}, "key"),
		makeTool("desktop_screenshot", "Screenshot this box. Call desktop_screenshot({}) for an image, or desktop_screenshot({output:\"file\"}) for a PNG path to pass to chat_message(files=[...]).", map[string]any{"output": map[string]any{"type": "string", "enum": []string{"image", "file"}, "default": "image"}}),
		makeTool("capture_window", `Capture an X11 window. Call capture_window({}) for the active window, or capture_window({window_id:"0x..."}).`, map[string]any{"window_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 10}}),
		makeTool("desktop_move", `Move the cursor. Call desktop_move({"x":10,"y":20}).`, point, "x", "y"),
		makeTool("desktop_click", `Click. Call desktop_click({"x":10,"y":20}); button is 1/2/3 and count is 1/2.`, map[string]any{"x": integer, "y": integer, "button": map[string]any{"type": "integer", "minimum": 1, "maximum": 3}, "count": map[string]any{"type": "integer", "minimum": 1, "maximum": 2}}, "x", "y"),
		makeTool("desktop_drag", `Drag. Call desktop_drag({"x":10,"y":20,"toX":100,"toY":200}).`, map[string]any{"x": integer, "y": integer, "toX": integer, "toY": integer}, "x", "y", "toX", "toY"),
		makeTool("desktop_scroll", `Scroll. Call desktop_scroll({"x":10,"y":20,"text":"down","count":1}).`, map[string]any{"x": integer, "y": integer, "text": map[string]any{"type": "string", "enum": []string{"up", "down", "left", "right"}}, "count": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}}, "x", "y", "text"),
		makeTool("desktop_type", `Type literal text. Call desktop_type({"text":"hello"}); use the secret tools for passwords.`, map[string]any{"text": map[string]any{"type": "string", "minLength": 1, "maxLength": 16384}}, "text"),
		makeTool("desktop_key", `Press keys. Call desktop_key({"keys":["ctrl","l"]}).`, map[string]any{"keys": map[string]any{"type": "array", "minItems": 1, "maxItems": 5, "items": map[string]any{"type": "string"}}}, "keys"),
	}
}

// ServeDesktopMCP is a local stdio adapter. stdout contains protocol only; the
// assignment captured at startup fences every subsequent operation.
func ServeDesktopMCP(ctx context.Context, assignment string, input io.Reader, output io.Writer) error {
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
			response["result"] = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}, "experimental": map[string]any{"claude/channel": map[string]any{}}}, "serverInfo": map[string]any{"name": "vmbox-desktop", "version": "0.2.0"}, "instructions": "Use chat_message for every user-facing reply; use chat_ask for choices. Busy state is automatic for normal replies; use set_busy for other work. Read ~/.config/vmbox/mcp-tools.md for all vmbox tool calls. HTTP tools: read ~/.local/share/vmbox/mcp-http.json, then POST JSON to {url}/tools/{name} with its Bearer token."}
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
			role := contact.Role
			if role == "" {
				role = "worker"
			}
			lines = append(lines, fmt.Sprintf("- %s | id %s | role %s | agent %s | %s | message %t | receive %t", contact.Name, contact.ID, role, contact.Agent, state, contact.CanMessage, contact.CanReceive))
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "Contacts you may message:\n" + strings.Join(lines, "\n")}}}, nil
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
	action.Action = strings.TrimPrefix(name, "desktop_")
	if action.Action == "click" && action.Count > 2 {
		return nil, fmt.Errorf("click count must be 1 or 2")
	}
	if err := DesktopInput(ctx, assignment, action); err != nil {
		return nil, err
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": "Action completed. Capture the screen to inspect its result."}}}, nil
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
