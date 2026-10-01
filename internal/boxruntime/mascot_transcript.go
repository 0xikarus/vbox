package boxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// clipMascotSample keeps roughly the last 500-2000 tokens, without splitting a
// UTF-8 rune. The controller never receives a full native transcript.
func clipMascotSample(text string) string {
	value := []byte(strings.TrimSpace(text))
	if len(value) > mascotSampleBytes {
		value = value[len(value)-mascotSampleBytes:]
		for len(value) > 0 && value[0]&0xc0 == 0x80 {
			value = value[1:]
		}
	}
	return strings.TrimSpace(string(value))
}

func mascotNativeSample(ctx context.Context, home, session, agent string) (string, error) {
	switch agent {
	case "codex":
		id, err := os.ReadFile(codexThreadFile(WorkspaceRoot(), session))
		if err != nil || !processID.MatchString(strings.TrimSpace(string(id))) {
			return "", fmt.Errorf("active Codex conversation unavailable")
		}
		return mascotCodexTranscript(home, strings.TrimSpace(string(id)))
	case "claude":
		id := os.Getenv("CLAUDE_CODE_SESSION_ID")
		if !claudeSessionID.MatchString(id) {
			return "", fmt.Errorf("active Claude conversation unavailable")
		}
		return mascotClaudeTranscript(home, WorkspaceDirectory(), id)
	case "opencode":
		return mascotOpenCodeTranscript(ctx, home, session)
	default:
		return "", fmt.Errorf("unsupported mascot transcript source")
	}
}

// Native JSONL files can contain very large tool records. Read only their tail,
// discard a partial first line, and parse at most the newest 128 records.
func mascotJSONLTail(path string) ([][]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("native transcript is not a regular file")
	}
	const window = 256 << 10
	start := info.Size() - window
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, window))
	if err != nil {
		return nil, err
	}
	if start > 0 {
		if cut := bytes.IndexByte(data, '\n'); cut >= 0 {
			data = data[cut+1:]
		} else {
			return nil, nil
		}
	}
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > 128 {
		lines = lines[len(lines)-128:]
	}
	return lines, nil
}

func appendMascotText(lines *[]string, role, value string) {
	if role != "assistant" && role != "tool" && role != "tool-output" && role != "user" {
		return
	}
	for _, raw := range strings.Split(value, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if len(line) > 1000 {
			line = line[:1000]
		}
		*lines = append(*lines, role+": "+line)
	}
}

func mascotContentText(raw json.RawMessage) string {
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return plain
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var texts []string
	for _, block := range blocks {
		switch block.Type {
		case "text", "input_text", "output_text":
			texts = append(texts, block.Text)
		case "tool_result":
			texts = append(texts, mascotContentText(block.Content))
		}
	}
	return strings.Join(texts, "\n")
}

func mascotToolLabel(name string, raw json.RawMessage) string {
	if strings.Contains(name, "__") {
		name = name[strings.LastIndex(name, "__")+2:]
	} else if strings.Contains(name, ".") {
		name = name[strings.LastIndex(name, ".")+1:]
	}
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil {
		raw = json.RawMessage(encoded)
	}
	var input map[string]json.RawMessage
	_ = json.Unmarshal(raw, &input)
	value := func(keys ...string) string {
		for _, key := range keys {
			var text string
			if json.Unmarshal(input[key], &text) == nil && text != "" {
				return text
			}
		}
		return ""
	}
	tool := strings.ToLower(name)
	switch {
	case tool == "chat_message":
		return "Messaging a box"
	case tool == "get_contacts":
		return "Checking contacts"
	case tool == "bash" || tool == "shell" || tool == "exec_command" || tool == "run_shell" || tool == "terminal":
		activity := mascotCommandActivity(value("command", "cmd"))
		if activity == "" {
			return ""
		}
		return mascotClipToolLabel("Running " + activity)
	case tool == "edit" || tool == "write" || tool == "apply_patch" || tool == "multi_edit":
		file := mascotToolBasename(value("file_path", "path", "filePath", "filename", "target_file"))
		if file == "" && tool == "apply_patch" {
			var patch string
			_ = json.Unmarshal(input["patch"], &patch)
			for _, line := range strings.Split(patch, "\n") {
				if target, ok := strings.CutPrefix(line, "*** Update File: "); ok {
					file = mascotToolBasename(target)
					break
				}
				if target, ok := strings.CutPrefix(line, "*** Add File: "); ok {
					file = mascotToolBasename(target)
					break
				}
			}
		}
		if file == "" {
			file = "file"
		}
		return mascotClipToolLabel("Editing " + file)
	case tool == "read" || tool == "read_file" || tool == "view" || tool == "view_image":
		file := mascotToolBasename(value("file_path", "path", "filePath", "filename"))
		if file == "" {
			file = "file"
		}
		return mascotClipToolLabel("Reading " + file)
	case tool == "websearch" || tool == "webfetch" || tool == "web_search" || tool == "web_fetch":
		return "Searching the web"
	case tool == "grep" || tool == "glob" || tool == "search" || tool == "search_query" || tool == "rg":
		return "Searching code"
	default:
		name = mascotSafeToolWord(name)
		if name == "" {
			name = "tool"
		}
		return mascotClipToolLabel("Using " + name)
	}
}

// MascotToolLabel returns a short, argument-free activity for transcript data.
func MascotToolLabel(name string, raw json.RawMessage) string {
	return mascotToolLabel(name, raw)
}

func mascotClipToolLabel(label string) string {
	runes := []rune(label)
	// The transcript line includes the six-character "tool: " prefix.
	if len(runes) > 42 {
		return string(runes[:42])
	}
	return label
}

func mascotSafeToolWord(value string) string {
	if value == "" {
		return ""
	}
	for _, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' || char == '+') {
			return ""
		}
	}
	return value
}

func mascotToolBasename(value string) string {
	file := path.Base(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
	if file == "." || file == "/" {
		return ""
	}
	return mascotSafeToolWord(file)
}

func mascotCommandActivity(command string) string {
	words := strings.Fields(command)
	if len(words) == 0 {
		return "command"
	}
	first := mascotSafeToolWord(path.Base(strings.ReplaceAll(words[0], "\\", "/")))
	if first == "" {
		return "command"
	}
	switch strings.ToLower(first) {
	case "cd", "echo", "sleep", "cat", "ls", "pwd", "true", "test":
		return ""
	}
	known := map[string]map[string]bool{
		"go":    {"test": true, "build": true, "vet": true, "run": true, "fmt": true, "mod": true, "generate": true},
		"npm":   {"run": true, "test": true, "install": true, "ci": true, "build": true},
		"pnpm":  {"run": true, "test": true, "install": true, "build": true},
		"yarn":  {"run": true, "test": true, "install": true, "build": true},
		"bun":   {"run": true, "test": true, "install": true, "build": true},
		"cargo": {"test": true, "build": true, "check": true, "fmt": true},
		"git":   {"status": true, "diff": true, "log": true, "fetch": true, "pull": true, "push": true, "rebase": true, "commit": true},
	}
	activity := first
	if len(words) > 1 && known[strings.ToLower(first)][words[1]] {
		activity += " " + words[1]
		if len(words) > 2 && (first == "npm" || first == "pnpm" || first == "yarn" || first == "bun") && words[1] == "run" {
			switch words[2] {
			case "build", "test", "lint", "check", "typecheck", "format", "dev", "start":
				activity += " " + words[2]
			}
		}
	}
	return activity
}

func mascotClaudeToolActivities(raw json.RawMessage) []string {
	var blocks []struct {
		Type  string          `json:"type"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var activities []string
	for _, block := range blocks {
		if block.Type == "tool_use" {
			if label := mascotToolLabel(block.Name, block.Input); label != "" {
				activities = append(activities, label)
			}
		}
	}
	return activities
}

func mascotCodexTranscript(home, id string) (string, error) {
	if !processID.MatchString(id) {
		return "", fmt.Errorf("invalid Codex conversation")
	}
	paths, err := filepath.Glob(filepath.Join(home, ".codex", "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl"))
	if err != nil || len(paths) == 0 {
		return "", fmt.Errorf("active Codex transcript unavailable")
	}
	records, err := mascotJSONLTail(paths[len(paths)-1])
	if err != nil {
		return "", err
	}
	var messages, events []string
	for _, line := range records {
		if len(line) > 64<<10 {
			continue
		}
		var record struct {
			Type    string `json:"type"`
			Payload struct {
				Type      string          `json:"type"`
				Role      string          `json:"role"`
				Content   json.RawMessage `json:"content"`
				Message   string          `json:"message"`
				Output    string          `json:"output"`
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		switch {
		case record.Type == "response_item" && record.Payload.Type == "message":
			appendMascotText(&messages, record.Payload.Role, mascotContentText(record.Payload.Content))
		case record.Type == "response_item" && record.Payload.Type == "function_call_output":
			appendMascotText(&messages, "tool-output", record.Payload.Output)
		case record.Type == "response_item" && record.Payload.Type == "function_call":
			if label := mascotToolLabel(record.Payload.Name, record.Payload.Arguments); label != "" {
				appendMascotText(&messages, "tool", label)
			}
		case record.Type == "event_msg" && record.Payload.Type == "agent_message":
			appendMascotText(&events, "assistant", record.Payload.Message)
		}
	}
	if len(messages) == 0 {
		messages = events
	}
	return clipMascotSample(strings.Join(messages, "\n")), nil
}

func mascotClaudeTranscript(home, workspace, id string) (string, error) {
	if !claudeSessionID.MatchString(id) {
		return "", fmt.Errorf("invalid Claude conversation")
	}
	project := "-" + strings.ReplaceAll(strings.TrimPrefix(filepath.Clean(workspace), "/"), "/", "-")
	records, err := mascotJSONLTail(filepath.Join(home, ".claude", "projects", project, id+".jsonl"))
	if err != nil {
		return "", err
	}
	var lines []string
	for _, line := range records {
		if len(line) > 64<<10 {
			continue
		}
		var record struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &record) != nil || record.IsSidechain {
			continue
		}
		if record.Type == "assistant" || record.Type == "user" {
			appendMascotText(&lines, record.Message.Role, mascotContentText(record.Message.Content))
			if record.Type == "assistant" {
				for _, activity := range mascotClaudeToolActivities(record.Message.Content) {
					appendMascotText(&lines, "tool", activity)
				}
			}
		}
	}
	return clipMascotSample(strings.Join(lines, "\n")), nil
}

// The OpenCode TUI plugin publishes a private Unix socket for this MCP session.
// It chooses the currently visible native session and extracts text through
// OpenCode's own message API; no terminal or session database probe is needed.
func mascotOpenCodeTranscript(ctx context.Context, home, session string) (string, error) {
	path := filepath.Join(home, ".local", "share", "vmbox", "opencode-tui", "mascot-"+session+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var marker struct {
		Socket   string `json:"socket"`
		Instance string `json:"instance"`
	}
	if json.Unmarshal(data, &marker) != nil || marker.Instance == "" || marker.Socket == "" || filepath.Dir(marker.Socket) != filepath.Dir(path) {
		return "", fmt.Errorf("invalid OpenCode transcript bridge")
	}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", marker.Socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://vmbox-tui/mascot-transcript", nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenCode transcript unavailable")
	}
	var result struct {
		Instance string `json:"instance"`
		Text     string `json:"text"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&result) != nil || result.Instance != marker.Instance {
		return "", fmt.Errorf("OpenCode transcript changed")
	}
	return clipMascotSample(result.Text), nil
}
