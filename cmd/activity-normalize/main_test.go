package main

import "testing"

func TestNormalizeTextUsesHarnessLabels(t *testing.T) {
	got := normalizeText("tool: Running S=private; export PATH=/tmp; cd /tmp && go test ./...\ntool: Running timeout 300 node --test suite\ntool: Editing app.go")
	want := "tool: Running go test\ntool: Running node tests\ntool: Editing app.go"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}
