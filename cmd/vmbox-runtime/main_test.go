package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

func TestReceiveFilesWritesOneChecksummedBatch(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "home", ".codex", "auth.json")
	second := filepath.Join(root, "workspace", "AGENTS.md")
	request := boxruntime.SyncRequest{Files: []boxruntime.SyncFile{
		{Path: first, Mode: "0600", Data: []byte("secret")},
		{Path: second, Mode: "0644", Data: []byte("instructions")},
	}}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := receiveFiles(bytes.NewReader(payload), root)
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256(payload)
	if digest != fmt.Sprintf("%x", expected[:]) {
		t.Fatalf("digest=%q", digest)
	}
	for path, want := range map[string]string{first: "secret", second: "instructions"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("file %s data=%q err=%v", path, data, err)
		}
	}
	if info, err := os.Stat(first); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("first mode=%v err=%v", info, err)
	}
}

func TestReceiveFilesRejectsDestinationOutsideRoot(t *testing.T) {
	root := t.TempDir()
	payload, _ := json.Marshal(boxruntime.SyncRequest{Files: []boxruntime.SyncFile{{Path: filepath.Join(root, "..", "escape"), Mode: "0600", Data: []byte("no")}}})
	if _, err := receiveFiles(bytes.NewReader(payload), root); err == nil {
		t.Fatal("sync accepted a destination outside its root")
	}
}

func TestPerformSetupConsolidatesCredentialAndAuthWork(t *testing.T) {
	type call struct {
		argv  []string
		stdin string
	}
	var calls []call
	execute := func(_ context.Context, stdin io.Reader, stdout, _ io.Writer, name string, args ...string) error {
		var input []byte
		if stdin != nil {
			input, _ = io.ReadAll(stdin)
		}
		calls = append(calls, call{argv: append([]string{name}, args...), stdin: string(input)})
		if name == "claude" {
			_, _ = io.WriteString(stdout, `{"loggedIn":true}`)
		}
		return nil
	}
	request := boxruntime.SetupRequest{
		Workspace:    "/data/workspace",
		GitHub:       &boxruntime.GitHubSetup{Host: "github.com", User: "octocat", Protocol: "ssh", Token: "github-secret"},
		Applications: []string{"codex", "claude", "codex"},
	}
	result, err := performSetup(context.Background(), request, execute)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Authentication, map[string]bool{"codex": true, "claude": true}) || len(calls) != 5 {
		t.Fatalf("result=%+v calls=%#v", result, calls)
	}
	if calls[0].stdin != "github-secret\n" || strings.Contains(strings.Join(calls[0].argv, " "), "github-secret") {
		t.Fatalf("GitHub token transport=%#v", calls[0])
	}
	if !reflect.DeepEqual(calls[2].argv, []string{"vmbox-entrypoint", "--configure-agent-trust", "/data/workspace"}) {
		t.Fatalf("trust argv=%#v", calls[2].argv)
	}
}

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
