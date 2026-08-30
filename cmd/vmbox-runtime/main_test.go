package main

import "testing"

func TestParseReport(t *testing.T) {
	tests := []struct {
		args          []string
		kind, message string
		wantErr       bool
	}{
		{args: []string{"working"}, kind: "progress", message: "working"},
		{args: []string{"--progress", "running", "tests"}, kind: "progress", message: "running tests"},
		{args: []string{"--needs-input", "which", "version?"}, kind: "needs_input", message: "which version?"},
		{args: []string{"--unknown", "message"}, wantErr: true},
		{args: []string{"--needs-input"}, wantErr: true},
	}
	for _, test := range tests {
		kind, message, err := parseReport(test.args)
		if (err != nil) != test.wantErr || kind != test.kind || message != test.message {
			t.Errorf("parseReport(%q) = %q, %q, %v", test.args, kind, message, err)
		}
	}
}
