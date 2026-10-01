package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/activityphrase"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

type snippet struct {
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	Candidates []string `json:"candidates,omitempty"`
}

var (
	activityURL       = regexp.MustCompile(`https?://[^\s<>"']+`)
	activityEmail     = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	activityAuth      = regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*(?:Bearer\s+)?[^\s,;]+`)
	activityKeyValue  = regexp.MustCompile(`(?i)\b(?:token|secret|password|passwd|api[_-]?key|authorization)\s*[:=]\s*[^\s,;]+`)
	activityKeyIs     = regexp.MustCompile(`(?i)\b(?:token|secret|password|api[_-]?key)\s+is\s+[^\s,;]+`)
	activityBearer    = regexp.MustCompile(`(?i)\bBearer\s+[^\s,;]+`)
	activityLongToken = regexp.MustCompile(`\b[A-Za-z0-9_\-]{16,}\b`)
	activityKeyPrefix = regexp.MustCompile(`(?i)\b(?:sk-[A-Za-z0-9_\-]{8,}|gh[pousr]_[A-Za-z0-9_]{8,}|AKIA[A-Z0-9]{16})\b`)
	activityPath      = regexp.MustCompile(`(?:/[A-Za-z0-9._\-]+){2,}`)
)

func anonymizeActivity(text string) string {
	return anonymizeActivityWithPatterns(text, nil)
}

func identifierPatterns(identifiers []string) []*regexp.Regexp {
	seen := make(map[string]bool)
	var patterns []*regexp.Regexp
	for _, identifier := range identifiers {
		identifier = strings.TrimSpace(identifier)
		if len([]rune(identifier)) >= 6 && !seen[strings.ToLower(identifier)] {
			seen[strings.ToLower(identifier)] = true
			patterns = append(patterns, regexp.MustCompile(`(?i)`+regexp.QuoteMeta(identifier)))
		}
	}
	return patterns
}

func anonymizeActivityWithPatterns(text string, identifiers []*regexp.Regexp) string {
	for _, identifier := range identifiers {
		text = identifier.ReplaceAllString(text, "[redacted]")
	}
	text = activityURL.ReplaceAllString(text, "[url]")
	text = activityEmail.ReplaceAllString(text, "[email]")
	text = activityAuth.ReplaceAllString(text, "[redacted]")
	text = activityKeyValue.ReplaceAllString(text, "[redacted]")
	text = activityKeyIs.ReplaceAllString(text, "[redacted]")
	text = activityBearer.ReplaceAllString(text, "[redacted]")
	text = activityKeyPrefix.ReplaceAllString(text, "[redacted]")
	text = activityLongToken.ReplaceAllString(text, "[redacted]")
	text = activityPath.ReplaceAllString(text, "[path]")
	return text
}

func configuredIdentifiers() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	root := filepath.Join(home, ".config", "vmbox")
	var identifiers []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > 1<<20 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var content any
		if json.Unmarshal(data, &content) != nil {
			return nil
		}
		var visit func(any)
		visit = func(value any) {
			switch typed := value.(type) {
			case map[string]any:
				for key, child := range typed {
					lower := strings.ToLower(key)
					if name, ok := child.(string); ok && (strings.Contains(lower, "profile") || strings.Contains(lower, "account") || lower == "name" || lower == "username" || lower == "displayname") {
						identifiers = append(identifiers, name)
					}
					visit(child)
				}
			case []any:
				for _, child := range typed {
					visit(child)
				}
			}
		}
		visit(content)
		return nil
	})
	return identifiers
}

func jsonlPaths(inputs []string) ([]string, error) {
	var paths []string
	for _, input := range inputs {
		matches, err := filepath.Glob(input)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			matches = []string{input}
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if !info.IsDir() {
				if strings.HasSuffix(match, ".jsonl") {
					paths = append(paths, match)
				}
				continue
			}
			err = filepath.WalkDir(match, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() && strings.HasSuffix(path, ".jsonl") {
					paths = append(paths, path)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func scanTranscript(path string, consume func([]byte)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) <= 256<<10 {
			consume(line)
		}
	}
	return scanner.Err()
}

func contentText(raw json.RawMessage) string {
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
			texts = append(texts, contentText(block.Content))
		}
	}
	return strings.Join(texts, "\n")
}

func codexTranscript(path string, emit func(string)) error {
	return scanTranscript(path, func(line []byte) {
		var record struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(line, &record) != nil {
			return
		}
		var payload struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Message   string          `json:"message"`
			Item      json.RawMessage `json:"item"`
		}
		if json.Unmarshal(record.Payload, &payload) != nil {
			return
		}
		switch {
		case record.Type == "response_item" && payload.Type == "message" && payload.Role == "assistant":
			emitAssistant(contentText(payload.Content), emit)
		case record.Type == "response_item" && payload.Type == "function_call":
			emitTool(boxruntime.MascotToolLabel(payload.Name, payload.Arguments), emit)
		case record.Type == "event_msg" && payload.Type == "agent_message":
			emitAssistant(payload.Message, emit)
		case record.Type == "event_msg" && payload.Type == "item_completed":
			var item struct {
				Type      string          `json:"type"`
				Content   json.RawMessage `json:"content"`
				Command   string          `json:"command"`
				Tool      string          `json:"tool"`
				Arguments json.RawMessage `json:"arguments"`
				Path      string          `json:"path"`
				Changes   json.RawMessage `json:"changes"`
			}
			if json.Unmarshal(payload.Item, &item) != nil {
				return
			}
			switch item.Type {
			case "AgentMessage":
				emitAssistant(contentText(item.Content), emit)
			case "CommandExecution":
				arg, _ := json.Marshal(map[string]string{"cmd": item.Command})
				emitTool(boxruntime.MascotToolLabel("exec_command", arg), emit)
			case "FileChange":
				file := firstChangedPath(item.Changes)
				arg, _ := json.Marshal(map[string]string{"file_path": file})
				emitTool(boxruntime.MascotToolLabel("Edit", arg), emit)
			case "McpToolCall":
				emitTool(boxruntime.MascotToolLabel(item.Tool, item.Arguments), emit)
			case "ImageView":
				arg, _ := json.Marshal(map[string]string{"path": item.Path})
				emitTool(boxruntime.MascotToolLabel("view", arg), emit)
			}
		}
	})
}

func firstChangedPath(raw json.RawMessage) string {
	var changes []struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &changes) == nil && len(changes) > 0 {
		return changes[0].Path
	}
	var files map[string]json.RawMessage
	if json.Unmarshal(raw, &files) == nil {
		var names []string
		for name := range files {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) > 0 {
			return names[0]
		}
	}
	return ""
}

func claudeTranscript(path string, emit func(string)) error {
	return scanTranscript(path, func(line []byte) {
		var record struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &record) != nil || record.IsSidechain || (record.Type != "assistant" && record.Type != "user") {
			return
		}
		var blocks []struct {
			Type    string          `json:"type"`
			Text    string          `json:"text"`
			Name    string          `json:"name"`
			Input   json.RawMessage `json:"input"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(record.Message.Content, &blocks) != nil {
			if record.Type == "assistant" {
				emitAssistant(contentText(record.Message.Content), emit)
			}
			return
		}
		for _, block := range blocks {
			switch block.Type {
			case "text":
				if record.Type == "assistant" {
					emitAssistant(block.Text, emit)
				}
			case "tool_use":
				if record.Type == "assistant" {
					emitTool(boxruntime.MascotToolLabel(block.Name, block.Input), emit)
				}
			case "tool_result":
				// Native tool results are user-role context, not agent activity.
				_ = contentText(block.Content)
			}
		}
	})
}

func emitAssistant(text string, emit func(string)) {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line != "" && len(line) <= 400 {
			emit("assistant: " + line)
		}
	}
}

func emitTool(label string, emit func(string)) {
	if label != "" {
		emit("tool: " + label)
	}
}

func extractSnippets(codex, claude inputPaths, out string, limit int, additionalRedactions ...string) (int, error) {
	return extractSnippetsMode(codex, claude, out, limit, false, additionalRedactions...)
}

// extractGeneratorSnippets samples after agent prose, including prose with no
// extractive verb candidate. These are the cases where the generator currently
// returns an empty activity phrase most often.
func extractGeneratorSnippets(codex, claude inputPaths, out string, limit int, additionalRedactions ...string) (int, error) {
	return extractSnippetsMode(codex, claude, out, limit, true, additionalRedactions...)
}

func extractSnippetsMode(codex, claude inputPaths, out string, limit int, agentTail bool, additionalRedactions ...string) (int, error) {
	codexPaths, err := jsonlPaths(codex)
	if err != nil {
		return 0, err
	}
	claudePaths, err := jsonlPaths(claude)
	if err != nil {
		return 0, err
	}
	if len(codexPaths)+len(claudePaths) == 0 {
		return 0, fmt.Errorf("no Codex or Claude JSONL files found")
	}
	random := rand.New(rand.NewSource(20261001))
	redactions := identifierPatterns(append(configuredIdentifiers(), additionalRedactions...))
	seen := make(map[string]bool)
	var selected []snippet
	count := 0
	collect := func(event string, history *[]string) {
		*history = append(*history, event)
		if len(*history) > 16 {
			*history = (*history)[len(*history)-16:]
		}
		if agentTail && !strings.HasPrefix(event, "assistant: ") {
			return
		}
		for _, width := range []int{4, 8, 12} {
			start := len(*history) - width
			if start < 0 {
				start = 0
			}
			text := boxruntime.MascotTranscriptEvidence(strings.Join((*history)[start:], "\n"))
			text = anonymizeActivityWithPatterns(text, redactions)
			if agentTail {
				text = activityphrase.NormalizeEvidence(text)
			}
			if text == "" || len(text) > 2400 {
				continue
			}
			var candidates []string
			for _, candidate := range activityphrase.Candidates(text) {
				if !strings.Contains(candidate.Text, "redacted") && !strings.Contains(candidate.Text, "[path]") {
					candidates = append(candidates, candidate.Text)
				}
			}
			if len(candidates) == 0 && !agentTail {
				continue
			}
			hash := sha256.Sum256([]byte(text))
			id := hex.EncodeToString(hash[:10])
			if seen[id] {
				continue
			}
			seen[id] = true
			count++
			sample := snippet{ID: id, Text: text, Candidates: candidates}
			if len(selected) < limit {
				selected = append(selected, sample)
			} else if position := random.Intn(count); position < limit {
				selected[position] = sample
			}
		}
	}
	for _, source := range []struct {
		paths []string
		read  func(string, func(string)) error
	}{{codexPaths, codexTranscript}, {claudePaths, claudeTranscript}} {
		for _, path := range source.paths {
			var history []string
			if err := source.read(path, func(event string) { collect(event, &history) }); err != nil {
				return 0, fmt.Errorf("extract %s: %w", path, err)
			}
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID < selected[j].ID })
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return 0, err
	}
	file, err := os.Create(out)
	if err != nil {
		return 0, err
	}
	writer := bufio.NewWriter(file)
	for _, sample := range selected {
		line, err := json.Marshal(sample)
		if err != nil {
			file.Close()
			return 0, err
		}
		if _, err := writer.Write(append(line, '\n')); err != nil {
			file.Close()
			return 0, err
		}
	}
	if err := writer.Flush(); err != nil {
		file.Close()
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	return len(selected), nil
}
