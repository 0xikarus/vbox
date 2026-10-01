package boxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMascotClientAgent(t *testing.T) {
	for name, want := range map[string]string{"codex": "codex", "Claude Code": "claude", "opencode": "opencode", "other": ""} {
		if got := mascotClientAgent(name); got != want {
			t.Fatalf("%q: got %q, want %q", name, got, want)
		}
	}
}

func TestMascotTranscriptEvidenceFiltersUserAndCode(t *testing.T) {
	sample := "user: Please fix the error\nassistant: I am inspecting it.\nassistant: ```text\nassistant: error: demonstration\nassistant: ```\nassistant: I am checking the patch."
	evidence := mascotTranscriptEvidence(sample)
	if strings.Contains(evidence, "Please fix") || strings.Contains(evidence, "demonstration") || strings.Contains(evidence, "assistant:") {
		t.Fatalf("transcript context leaked into evidence: %q", evidence)
	}
	if evidence != "I am inspecting it.\nI am checking the patch." {
		t.Fatalf("unexpected evidence: %q", evidence)
	}
	if got := mascotTranscriptEvidence("assistant: Tests passed.\ntool: Running tool"); got != "Tests passed.\ntool: Running tool" {
		t.Fatalf("active native tool evidence: %q", got)
	}
	if got := mascotTranscriptEvidence("assistant: ```sh\nassistant: $ export TOKEN=abc\ntool: Running go test\nassistant: ```"); got != "tool: Running go test" {
		t.Fatalf("tool activity inside code fence was lost: %q", got)
	}
	if got := mascotTranscriptEvidence("tool-output: $ export TOKEN=abc\ntool: Running export"); got != "tool: Running export" {
		t.Fatalf("tool output was mistaken for safe activity: %q", got)
	}
}

func TestMascotMCPSessionUsesBoxBindingWithoutTmux(t *testing.T) {
	t.Setenv("VMBOX_CHAT_SESSION", "claude-managed")
	session, err := mascotMCPSession("claude")
	if err != nil || session != "claude-managed" {
		t.Fatalf("session=%q err=%v", session, err)
	}
}

func writeMascotCodexFixture(t *testing.T, home, root, session, id string) {
	t.Helper()
	path := codexThreadFile(root, session)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(home, ".codex", "sessions", "2026", "10", "01", "rollout-2026-10-01T00-00-00-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	content := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Please fix the error"}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Fixed the build. All tests passed."}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"go test ./...\"}"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMascotReadsActiveCodexAndClaudeTranscripts(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("VMBOX_WORKSPACE_ROOT", root)
	id := "01234567-89ab-cdef-0123-456789abcdef"
	writeMascotCodexFixture(t, home, root, "codex-managed", id)
	text, err := mascotNativeSample(context.Background(), home, "codex-managed", "codex")
	if err != nil || !strings.Contains(text, "assistant: Fixed the build. All tests passed.") || !strings.Contains(text, "user: Please fix the error") || !strings.Contains(text, "tool: Running go test") {
		t.Fatalf("Codex sample=%q err=%v", text, err)
	}
	if _, err := mascotNativeSample(context.Background(), home, "codex-other", "codex"); err == nil {
		t.Fatal("another Codex session reused the active transcript")
	}
	project := "-" + strings.ReplaceAll(strings.TrimPrefix(WorkspaceDirectory(), "/"), "/", "-")
	path := filepath.Join(home, ".claude", "projects", project, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	content := `{"type":"user","message":{"role":"user","content":"Why did it fail?"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"The retry succeeded."},{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", id)
	text, err = mascotNativeSample(context.Background(), home, "claude-managed", "claude")
	if err != nil || !strings.Contains(text, "assistant: The retry succeeded.") || !strings.Contains(text, "user: Why did it fail?") || !strings.Contains(text, "tool: Running go test") {
		t.Fatalf("Claude sample=%q err=%v", text, err)
	}
}

func TestMascotReadsVisibleOpenCodeBridgeWithoutTmux(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "vbm-mascot-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	dir := filepath.Join(home, ".local", "share", "vmbox", "opencode-tui")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "test.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mascot-transcript" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"instance":"visible-one","text":"assistant: Tests passed."}`))
	})}
	go server.Serve(listener)
	defer server.Close()
	marker, _ := json.Marshal(map[string]string{"socket": socket, "instance": "visible-one"})
	if err := os.WriteFile(filepath.Join(dir, "mascot-opencode-managed.json"), marker, 0600); err != nil {
		t.Fatal(err)
	}
	text, err := mascotNativeSample(context.Background(), home, "opencode-managed", "opencode")
	if err != nil || text != "assistant: Tests passed." {
		t.Fatalf("OpenCode sample=%q err=%v", text, err)
	}
}

func TestMascotHeartbeatSendsNativeExcerptToController(t *testing.T) {
	id := "01234567-89ab-cdef-0123-456789abcdef"
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_WORKSPACE_ROOT", root)
	writeMascotCodexFixture(t, home, root, "codex-managed", id)
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/agent-desktop/mascot-observation" || r.Header.Get("Authorization") != "DesktopAgent "+strings.Repeat("a", 64) {
			t.Errorf("bad route or auth: %s", r.URL.Path)
		}
		var request struct{ Session, Text string }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Session != "codex-managed" || !strings.Contains(request.Text, "Fixed the build") || strings.Contains(request.Text, "user:") || strings.Contains(request.Text, "assistant:") || len(request.Text) > mascotSampleBytes {
			t.Errorf("bad heartbeat: %+v, %v", request, err)
		}
		_, _ = w.Write([]byte(`{"mood":"happy","activity":"idle"}`))
	}))
	defer server.Close()
	cert := filepath.Join(home, "test-ca.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", cert)
	config, _ := json.Marshal(DesktopAgentConfig{Controller: server.URL, Assignment: "assignment", Token: strings.Repeat("a", 64)})
	if err := writeTextAtomic(filepath.Join(home, ".config", "vmbox", "desktop-agent.json"), string(config), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	state, err := sendMascotHeartbeat(context.Background(), "assignment", home, "codex-managed", "codex", mascotHeartbeatState{}, now)
	if err != nil || state.text == "" || !state.sentAt.Equal(now) {
		t.Fatalf("first observation=%+v err=%v", state, err)
	}
	state, err = sendMascotHeartbeat(context.Background(), "assignment", home, "codex-managed", "codex", state, now.Add(29*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("unchanged transcript sent %d observations before keepalive", requests.Load())
	}
	state, err = sendMascotHeartbeat(context.Background(), "assignment", home, "codex-managed", "codex", state, now.Add(30*time.Second))
	if err != nil || !state.sentAt.Equal(now.Add(30*time.Second)) || requests.Load() != 2 {
		t.Fatalf("unchanged transcript missed 30s keepalive: state=%+v requests=%d err=%v", state, requests.Load(), err)
	}
}

func TestDesktopMCPAutomaticallySendsMascotHeartbeatWithoutToolCall(t *testing.T) {
	id := "01234567-89ab-cdef-0123-456789abcdef"
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_WORKSPACE_ROOT", root)
	t.Setenv("VMBOX_CHAT_SESSION", "codex-managed")
	writeMascotCodexFixture(t, home, root, "codex-managed", id)
	received := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent-desktop/mascot-observation" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var request struct{ Session, Text string }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		select {
		case received <- request.Session + "\n" + request.Text:
		default:
		}
		_, _ = w.Write([]byte(`{"mood":"happy","activity":"idle"}`))
	}))
	defer server.Close()
	cert := filepath.Join(home, "test-ca.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", cert)
	config, _ := json.Marshal(DesktopAgentConfig{Controller: server.URL, Assignment: "assignment", Token: strings.Repeat("a", 64)})
	if err := writeTextAtomic(filepath.Join(home, ".config", "vmbox", "desktop-agent.json"), string(config), 0600); err != nil {
		t.Fatal(err)
	}
	originalTmux := tmuxCommand
	tmuxCommand = func(context.Context, string, ...string) ([]byte, error) {
		t.Error("mascot heartbeat checked tmux")
		return nil, nil
	}
	t.Cleanup(func() { tmuxCommand = originalTmux })
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- serveDesktopMCP(ctx, "assignment", reader, &bytes.Buffer{}, allDesktopToolPolicy) }()
	if _, err := io.WriteString(writer, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"codex"}}}`+"\n"+
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-received:
		if !strings.HasPrefix(text, "codex-managed\n") || !strings.Contains(text, "Fixed the build") || strings.Contains(text, "user:") || strings.Contains(text, "assistant:") {
			t.Fatalf("unexpected heartbeat: %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MCP process did not send the native transcript heartbeat")
	}
	_ = writer.Close()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MCP process did not stop")
	}
}
