package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractSnippetsFromCodexAndClaude(t *testing.T) {
	dir := t.TempDir()
	codex := filepath.Join(dir, "codex.jsonl")
	claude := filepath.Join(dir, "claude.jsonl")
	codexLines := `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I'm fixing the build for user@example.com."}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"export TOKEN=abc && curl -H 'Authorization: Bearer private' https://example.test/?token=abc\"}"}}` + "\n"
	claudeLines := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Now review the build at https://example.test/?token=abc."},{"type":"tool_use","name":"Edit","input":{"file_path":"/private/work/config.go"}}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"$ export TOKEN=abc"}]}}` + "\n"
	if err := os.WriteFile(codex, []byte(codexLines), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte(claudeLines), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "snippets.jsonl")
	count, err := extractSnippets(inputPaths{codex}, inputPaths{claude}, out, 100)
	if err != nil || count < 2 {
		t.Fatalf("extracted %d snippets: %v", count, err)
	}
	file, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var foundEdit, foundExport bool
	for scanner.Scan() {
		var sample snippet
		if err := json.Unmarshal(scanner.Bytes(), &sample); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"user@example.com", "https://", "TOKEN=abc", "Bearer private", "/private/work/"} {
			if strings.Contains(sample.Text, forbidden) {
				t.Fatalf("extractor leaked %q in %q", forbidden, sample.Text)
			}
		}
		for _, candidate := range sample.Candidates {
			foundEdit = foundEdit || candidate == "Editing config.go"
			foundExport = foundExport || candidate == "Running curl"
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !foundEdit || !foundExport {
		t.Fatalf("tool candidates missing: edit=%t export=%t", foundEdit, foundExport)
	}
}

func TestAnonymizeActivityRemovesCredentialsAndLinks(t *testing.T) {
	text := anonymizeActivity("Authorization: Bearer private123 TOKEN=abc sk-proj_12345678901234567890 user@example.com https://example.test/?key=abc")
	for _, forbidden := range []string{"private123", "TOKEN=abc", "sk-proj", "user@example.com", "https://", "key=abc"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("anonymization retained %q: %q", forbidden, text)
		}
	}
}

func TestConfiguredProfileNameIsRedacted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	config := filepath.Join(home, ".config", "vmbox", "profiles.json")
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"profileName":"horsebox"}`), 0600); err != nil {
		t.Fatal(err)
	}
	text := anonymizeActivityWithPatterns("Checking horsebox", identifierPatterns(configuredIdentifiers()))
	if strings.Contains(text, "horsebox") {
		t.Fatalf("profile identifier leaked: %q", text)
	}
}

func TestExtractGeneratorSnippetsKeepsProseWithoutCandidates(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "codex.jsonl")
	lines := `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The merge is complete."}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"go test ./...\"}"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The owner has the screenshots."}]}}` + "\n"
	if err := os.WriteFile(input, []byte(lines), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "generator.jsonl")
	count, err := extractGeneratorSnippets(inputPaths{input}, nil, out, 100)
	if err != nil || count != 2 {
		t.Fatalf("extracted %d prose snippets: %v", count, err)
	}
	file, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var sample snippet
		if err := json.Unmarshal(scanner.Bytes(), &sample); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(sample.Text, "The owner has the screenshots.") && !strings.HasSuffix(sample.Text, "The merge is complete.") {
			t.Fatalf("sample ends in tool activity: %q", sample.Text)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}
