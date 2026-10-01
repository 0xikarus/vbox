package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/activityphrase"
)

func TestNormalizeTextUsesHarnessLabels(t *testing.T) {
	got := activityphrase.NormalizeEvidence("tool: Running S=private; export PATH=/tmp; cd /tmp && go test ./...\ntool: Running timeout 300 node --test suite\ntool: Editing app.go")
	want := "tool: Running go test\ntool: Running node tests\ntool: Editing app.go"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRunPreservesSnippetFields(t *testing.T) {
	dir := t.TempDir()
	input, output := filepath.Join(dir, "input.jsonl"), filepath.Join(dir, "output.jsonl")
	if err := os.WriteFile(input, []byte(`{"id":"one","text":"tool: Running cd /tmp\ntool: Using [redacted]","candidates":["keep"]}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(input, output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]json.RawMessage
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatal(err)
	}
	var content string
	if err := json.Unmarshal(row["text"], &content); err != nil {
		t.Fatal(err)
	}
	if content != "tool: Using a tool" || string(row["candidates"]) != `["keep"]` {
		t.Fatalf("normalized row = %s", data)
	}
}
