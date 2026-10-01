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
		{"namespaced MCP", "mcp__vmbox-desktop__chat_message", "mcp__vmbox-desktop__chat_message", "Messaging a box", map[string]string{"text": "private data"}},
		{"contact lookup", "mcp__vmbox-desktop__get_contacts", "mcp__vmbox-desktop__get_contacts", "Checking contacts", map[string]string{}},
		{"trivial shell", "exec_command", "Bash", "Running command", map[string]string{"cmd": "cd /private/work", "command": "cd /private/work"}},
		{"secret command", "exec_command", "Bash", "Running curl", map[string]string{"cmd": "export TOKEN=abc && curl -H 'Authorization: Bearer secret' https://example.test", "command": "export TOKEN=abc && curl -H 'Authorization: Bearer secret' https://example.test"}},
		{"prefix chain", "exec_command", "Bash", "Running go test", map[string]string{"cmd": "S=abc; export PATH=/private/bin; cd /private/work && go test ./...", "command": "S=abc; export PATH=/private/bin; cd /private/work && go test ./..."}},
		{"node tests", "exec_command", "Bash", "Running node tests", map[string]string{"cmd": "timeout 300 node --test tests/browser", "command": "timeout 300 node --test tests/browser"}},
		{"gh merge", "exec_command", "Bash", "Running gh pr merge", map[string]string{"cmd": "env GITHUB_TOKEN=abc sudo nice -n 5 time gh pr merge 390", "command": "env GITHUB_TOKEN=abc sudo nice -n 5 time gh pr merge 390"}},
		{"git push", "exec_command", "Bash", "Running git push", map[string]string{"cmd": "cd /private/work || git push origin branch", "command": "cd /private/work || git push origin branch"}},
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
			want := ""
			if tc.want != "" {
				want = "tool: " + tc.want
			}
			if err != nil || codex != want {
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
			if err != nil || claude != want {
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

func TestMascotTrivialShellCommandsUseGenericLabel(t *testing.T) {
	for _, command := range []string{"cd /tmp", "echo hello", "sleep 1", "cat file", "ls -la", "pwd", "true", "test -f file"} {
		input, _ := json.Marshal(map[string]string{"command": command})
		if got := MascotToolLabel("Bash", input); got != "Running command" {
			t.Errorf("%q produced %q", command, got)
		}
	}
}

func TestMascotGoldenNativeEvidence(t *testing.T) {
	const id = "01234567-89ab-cdef-0123-456789abcdef"
	tests := []struct {
		name, fixture, want string
		read                func(string) (string, error)
		path                func(string) string
	}{
		{
			name: "Claude", fixture: "testdata/mascot_claude.jsonl",
			want: "I found the failing build.\nI am updating the test.\nI will rerun it now.\ntool: Running go test",
			read: func(home string) (string, error) { return mascotClaudeTranscript(home, "/private/workspace", id) },
			path: func(home string) string {
				return filepath.Join(home, ".claude", "projects", "-private-workspace", id+".jsonl")
			},
		},
		{
			name: "Codex", fixture: "testdata/mascot_codex.jsonl",
			want: "I am checking mobile gestures.\nThe browser suite is next.\nI will inspect the result.\ntool: Running node tests",
			read: func(home string) (string, error) { return mascotCodexTranscript(home, id) },
			path: func(home string) string {
				return filepath.Join(home, ".codex", "sessions", "2026", "10", "01", "rollout-2026-10-01T00-00-00-"+id+".jsonl")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			fixture, err := os.ReadFile(tc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			path := tc.path(home)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, fixture, 0600); err != nil {
				t.Fatal(err)
			}
			sample, err := tc.read(home)
			if err != nil {
				t.Fatal(err)
			}
			if got := mascotTranscriptEvidence(sample); got != tc.want {
				t.Fatalf("evidence = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMascotEvidenceKeepsProseBeforeToolNoise(t *testing.T) {
	const id = "01234567-89ab-cdef-0123-456789abcdef"
	home := t.TempDir()
	fixture, err := os.ReadFile("testdata/mascot_claude.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "projects", "-private-workspace", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	tool := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"timeout 300 node --test suite"}},{"type":"tool_use","name":"Bash","input":{"command":"timeout 300 node --test suite"}},{"type":"tool_use","name":"Bash","input":{"command":"timeout 300 node --test suite"}},{"type":"tool_use","name":"Bash","input":{"command":"timeout 300 node --test suite"}}]}}` + "\n"
	if err := os.WriteFile(path, append(fixture, []byte(strings.Repeat(tool, 100))...), 0600); err != nil {
		t.Fatal(err)
	}
	sample, err := mascotClaudeTranscript(home, "/private/workspace", id)
	if err != nil {
		t.Fatal(err)
	}
	evidence := mascotTranscriptEvidence(sample)
	for _, want := range []string{"I found the failing build.", "I am updating the test.", "I will rerun it now.", "tool: Running node tests"} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("lost %q after tool noise: %q", want, evidence)
		}
	}
	if len(sample) > mascotSampleBytes || len(evidence) > mascotSampleBytes || strings.Count(evidence, "tool: Running node tests") != 1 {
		t.Fatalf("noise not collapsed or sample unbounded: sample=%d evidence=%q", len(sample), evidence)
	}
}
