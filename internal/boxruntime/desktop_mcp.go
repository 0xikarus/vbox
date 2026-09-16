package boxruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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
		makeTool("chat_reply", "Send your response to the vmbox Agent chat. Call this once for each completed response, including any image files the user should receive.", map[string]any{"replyTo": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "text": map[string]any{"type": "string", "minLength": 1, "maxLength": 100000}, "files": map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string"}}}, "replyTo", "text"),
		makeTool("chat_ask", "Ask the user to choose one or more options in vmbox Agent chat when their decision is required.", map[string]any{"replyTo": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "question": map[string]any{"type": "string", "minLength": 1, "maxLength": 4000}, "choices": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 500}}, "multiple": map[string]any{"type": "boolean"}}, "replyTo", "question", "choices"),
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
			response["result"] = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}, "experimental": map[string]any{"claude/channel": map[string]any{}}}, "serverInfo": map[string]any{"name": "vmbox-desktop", "version": "0.2.0"}, "instructions": "Messages from vmbox Agent chat arrive as channel messages. Use chat_reply for every response the user should receive, and chat_ask when the user must choose."}
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
	readyDir := filepath.Join(home, ".local", "share", "vmbox", "chat", "channel-ready")
	if os.MkdirAll(readyDir, 0700) != nil {
		return
	}
	ready := filepath.Join(readyDir, session)
	defer os.Remove(ready)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		_ = os.WriteFile(ready, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0600)
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
	if name == "chat_reply" {
		var request struct {
			ReplyTo string   `json:"replyTo"`
			Text    string   `json:"text"`
			Files   []string `json:"files"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Text) == "" || len(request.Text) > 100_000 {
			return nil, fmt.Errorf("provide replyTo and response text")
		}
		if err := validateTmuxToken("replyTo", request.ReplyTo); err != nil {
			return nil, err
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
			Question string   `json:"question"`
			Choices  []string `json:"choices"`
			Multiple bool     `json:"multiple"`
		}
		if json.Unmarshal(args, &request) != nil || strings.TrimSpace(request.Question) == "" || len(request.Choices) < 1 || len(request.Choices) > 20 {
			return nil, fmt.Errorf("provide replyTo, question, and choices")
		}
		if err := validateTmuxToken("replyTo", request.ReplyTo); err != nil {
			return nil, err
		}
		for _, choice := range request.Choices {
			if strings.TrimSpace(choice) == "" || len(choice) > 500 {
				return nil, fmt.Errorf("invalid choice")
			}
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
