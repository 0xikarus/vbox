package boxruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMascotNativeToolActivities(t *testing.T) {
	cases := []struct {
		name, codexTool, claudeTool, want string
		input                             map[string]string
	}{
		{"shell go", "exec_command", "Bash", "Running go test", map[string]string{"cmd": "go test ./...", "command": "go test ./..."}},
		{"shell npm", "exec_command", "Bash", "Running npm run build", map[string]string{"cmd": "npm run build -- --secret=abc", "command": "npm run build -- --secret=abc"}},
		{"edit", "Edit", "Edit", "Editing service.go", map[string]string{"file_path": "/private/work/service.go"}},
		{"write", "Write", "Write", "Editing result.txt", map[string]string{"file_path": "/private/work/result.txt"}},
		{"apply patch", "apply_patch", "apply_patch", "Editing config.yaml", map[string]string{"patch": "*** Begin Patch\n*** Update File: /private/work/config.yaml\n+token: abc\n*** End Patch"}},
		{"read", "read_file", "Read", "Reading service.go", map[string]string{"file_path": "/private/work/service.go"}},
		{"view", "view_image", "view", "Reading image.png", map[string]string{"path": "/private/work/image.png"}},
		{"grep", "grep", "Grep", "Searching code", map[string]string{"pattern": "TOKEN=abc"}},
		{"glob", "glob", "Glob", "Searching code", map[string]string{"pattern": "/private/**"}},
		{"search", "search", "search", "Searching code", map[string]string{"query": "secret"}},
		{"web search", "web_search", "WebSearch", "Searching the web", map[string]string{"query": "secret"}},
		{"web fetch", "web_fetch", "WebFetch", "Searching the web", map[string]string{"url": "https://example.test/token=abc"}},
		{"other", "TaskTool", "TaskTool", "Using TaskTool", map[string]string{"prompt": "private data"}},
		{"namespaced MCP", "mcp__vmbox-desktop__chat_message", "mcp__vmbox-desktop__chat_message", "Using chat_message", map[string]string{"text": "private data"}},
		{"secret command", "exec_command", "Bash", "Running export", map[string]string{"cmd": "export TOKEN=abc && curl -H 'Authorization: Bearer secret' https://example.test", "command": "export TOKEN=abc && curl -H 'Authorization: Bearer secret' https://example.test"}},
	}
	const id = "01234567-89ab-cdef-0123-456789abcdef"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			input, err := json.Marshal(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			codexRecord, err := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": tc.codexTool, "arguments": string(input)}})
			if err != nil {
				t.Fatal(err)
			}
			codexPath := filepath.Join(home, ".codex", "sessions", "2026", "10", "01", "rollout-2026-10-01T00-00-00-"+id+".jsonl")
			if err := os.MkdirAll(filepath.Dir(codexPath), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(codexPath, append(codexRecord, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			codex, err := mascotCodexTranscript(home, id)
			if err != nil || codex != "tool: "+tc.want {
				t.Fatalf("Codex: got %q, want %q, err=%v", codex, tc.want, err)
			}
			claudeRecord, err := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "name": tc.claudeTool, "input": tc.input}}}})
			if err != nil {
				t.Fatal(err)
			}
			workspace := "/private/workspace"
			claudePath := filepath.Join(home, ".claude", "projects", "-private-workspace", id+".jsonl")
			if err := os.MkdirAll(filepath.Dir(claudePath), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(claudePath, append(claudeRecord, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			claude, err := mascotClaudeTranscript(home, workspace, id)
			if err != nil || claude != "tool: "+tc.want {
				t.Fatalf("Claude: got %q, want %q, err=%v", claude, tc.want, err)
			}
			for _, sample := range []string{codex, claude} {
				if strings.Contains(sample, "abc") || strings.Contains(sample, "Authorization") || strings.Contains(sample, "/private/") || len([]rune(strings.TrimPrefix(sample, "tool: "))) > 48 {
					t.Fatalf("tool activity leaked arguments or path: %q", sample)
				}
			}
		})
	}
}
