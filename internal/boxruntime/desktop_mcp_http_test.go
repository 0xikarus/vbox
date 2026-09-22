package boxruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func desktopMCPHTTPRequest(t *testing.T, handler http.Handler, method, target, token, body string) (int, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	decoded := map[string]any{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("%s %s returned %q", method, target, recorder.Body.String())
	}
	return recorder.Code, decoded
}

// Loopback is shared between boxes on a worker, so an unauthenticated caller
// must get nothing, not even the tool list.
func TestDesktopMCPHTTPRequiresToken(t *testing.T) {
	handler := desktopMCPHTTPHandler("assignment", "secret-token", allDesktopToolPolicy)
	for _, target := range []string{"/", "/tools", "/tools/take_screenshot", "/prompt"} {
		status, body := desktopMCPHTTPRequest(t, handler, http.MethodGet, target, "", "")
		if status != http.StatusUnauthorized {
			t.Fatalf("%s without a token returned %d", target, status)
		}
		if _, ok := body["error"]; !ok {
			t.Fatalf("%s gave no reason: %v", target, body)
		}
	}
	status, _ := desktopMCPHTTPRequest(t, handler, http.MethodGet, "/tools", "wrong-token", "")
	if status != http.StatusUnauthorized {
		t.Fatalf("a wrong token returned %d", status)
	}
}

func TestDesktopMCPHTTPPromptDeliversToRunningClaudeConversation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	original := tmuxCommand
	t.Cleanup(func() { tmuxCommand = original })
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "list-sessions -F #{session_name}":
			return []byte("agent-session\n"), nil
		case "show-environment -t agent-session " + taskAgentEnvironment:
			return []byte(taskAgentEnvironment + "=claude\n"), nil
		default:
			return nil, fmt.Errorf("unexpected tmux command: %v", args)
		}
	}
	handler := desktopMCPHTTPHandler("assignment", "secret-token", allDesktopToolPolicy)
	status, response := desktopMCPHTTPRequest(t, handler, http.MethodPost, "/prompt", "secret-token", `{"text":"run the local check"}`)
	if status != http.StatusAccepted || response["accepted"] != true || response["session"] != "agent-session" {
		t.Fatalf("prompt returned %d %v", status, response)
	}
	inbound, _, found, err := nextChatInbound(home, "agent-session")
	if err != nil || !found || inbound.Text != "run the local check" {
		t.Fatalf("inbound=%+v found=%v err=%v", inbound, found, err)
	}
}

func TestDesktopMCPHTTPPromptRejectsNonRunningAndAmbiguousInput(t *testing.T) {
	original := tmuxCommand
	t.Cleanup(func() { tmuxCommand = original })
	tmuxCommand = func(context.Context, string, ...string) ([]byte, error) {
		return nil, fmt.Errorf("no tmux server")
	}
	handler := desktopMCPHTTPHandler("assignment", "secret-token", allDesktopToolPolicy)
	for name, body := range map[string]string{
		"empty":    `{"text":""}`,
		"unknown":  `{"text":"hello","wake":true}`,
		"trailing": `{"text":"hello"}{"text":"again"}`,
	} {
		t.Run(name, func(t *testing.T) {
			status, _ := desktopMCPHTTPRequest(t, handler, http.MethodPost, "/prompt", "secret-token", body)
			if status != http.StatusBadRequest {
				t.Fatalf("invalid prompt returned %d", status)
			}
		})
	}
	status, response := desktopMCPHTTPRequest(t, handler, http.MethodPost, "/prompt", "secret-token", `{"text":"hello"}`)
	if status != http.StatusConflict || !strings.Contains(response["error"].(string), "no agent conversation") {
		t.Fatalf("non-running prompt returned %d %v", status, response)
	}
	if status, _ := desktopMCPHTTPRequest(t, handler, http.MethodGet, "/prompt", "secret-token", ""); status != http.StatusMethodNotAllowed {
		t.Fatalf("GET prompt returned %d", status)
	}
}

// Readiness must answer before a caller has the token, so the start-up probe
// can tell "not listening yet" from "listening".
func TestDesktopMCPHTTPHealthIsOpen(t *testing.T) {
	status, body := desktopMCPHTTPRequest(t, desktopMCPHTTPHandler("assignment", "secret-token", allDesktopToolPolicy), http.MethodGet, "/health", "", "")
	if status != http.StatusOK || body["ok"] != true {
		t.Fatalf("health returned %d %v", status, body)
	}
}

func TestDesktopMCPHTTPListsTheSameToolsAsMCP(t *testing.T) {
	status, body := desktopMCPHTTPRequest(t, desktopMCPHTTPHandler("assignment", "secret-token", allDesktopToolPolicy), http.MethodGet, "/tools", "secret-token", "")
	if status != http.StatusOK {
		t.Fatalf("tools returned %d", status)
	}
	listed, _ := body["tools"].([]any)
	if len(listed) != len(desktopMCPTools()) {
		t.Fatalf("listed %d of %d tools", len(listed), len(desktopMCPTools()))
	}
	names := map[string]bool{}
	for _, value := range listed {
		tool, _ := value.(map[string]any)
		names[tool["name"].(string)] = true
	}
	for _, tool := range desktopMCPTools() {
		if !names[tool["name"].(string)] {
			t.Fatalf("%s is missing over HTTP", tool["name"])
		}
	}
	for _, value := range listed {
		tool, _ := value.(map[string]any)
		if tool["name"] != "take_screenshot" {
			continue
		}
		schema := tool["inputSchema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		output := properties["output"].(map[string]any)
		if output["default"] != "image" {
			t.Fatalf("take_screenshot default output = %v", output["default"])
		}
		if got := output["enum"].([]any); len(got) != 2 || got[0] != "image" || got[1] != "file" {
			t.Fatalf("take_screenshot output enum = %v", got)
		}
	}
}

func TestDesktopMCPHTTPRejectsToolOutsideBoxPolicy(t *testing.T) {
	resolve := func(context.Context, string) (map[string]bool, error) {
		return map[string]bool{"take_screenshot": true}, nil
	}
	handler := desktopMCPHTTPHandler("assignment", "secret-token", resolve)
	status, body := desktopMCPHTTPRequest(t, handler, http.MethodPost, "/tools/click_mouse", "secret-token", `{"x":10,"y":20}`)
	if status != http.StatusForbidden || !strings.Contains(body["error"].(string), "not allowed") {
		t.Fatalf("disallowed tool returned %d %v", status, body)
	}
}

func TestDesktopMCPHTTPRejectsUnknownToolsAndArguments(t *testing.T) {
	handler := desktopMCPHTTPHandler("assignment", "secret-token", allDesktopToolPolicy)
	if status, _ := desktopMCPHTTPRequest(t, handler, http.MethodPost, "/tools/desktop_launch_missiles", "secret-token", `{}`); status != http.StatusBadRequest {
		t.Fatalf("unknown tool returned %d", status)
	}
	if status, _ := desktopMCPHTTPRequest(t, handler, http.MethodGet, "/tools/click_mouse?z=1", "secret-token", ""); status != http.StatusBadRequest {
		t.Fatalf("unknown argument returned %d", status)
	}
	status, body := desktopMCPHTTPRequest(t, handler, http.MethodPost, "/tools/take_screenshot", "secret-token", `{"output":"path"}`)
	if status != http.StatusBadRequest || !strings.Contains(body["error"].(string), "image or file") {
		t.Fatalf("invalid screenshot output returned %d %v", status, body)
	}
	status, body = desktopMCPHTTPRequest(t, handler, http.MethodPost, "/tools/click_mouse", "secret-token", `{"x":10}`)
	if status != http.StatusBadRequest || !strings.Contains(body["error"].(string), "y") {
		t.Fatalf("missing argument returned %d %v", status, body)
	}
	if status, _ := desktopMCPHTTPRequest(t, handler, http.MethodDelete, "/tools/click_mouse", "secret-token", ""); status != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE returned %d", status)
	}
}

// A shell script writes query strings far more easily than JSON, so GET values
// are converted to the types each tool's schema asks for.
func TestDesktopMCPHTTPQueryArgumentsFollowTheSchema(t *testing.T) {
	raw, err := desktopMCPQueryArguments(map[string][]string{"x": {"10"}, "y": {"20"}, "button": {"3"}}, "click_mouse")
	if err != nil {
		t.Fatal(err)
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["x"] != float64(10) || decoded["y"] != float64(20) || decoded["button"] != float64(3) {
		t.Fatalf("integers were not converted: %v", decoded)
	}
	raw, err = desktopMCPQueryArguments(map[string][]string{"question": {"ship it?"}, "choices": {"yes", "no"}, "multiple": {"true"}}, "chat_ask")
	if err != nil {
		t.Fatal(err)
	}
	decoded = map[string]any{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if choices, _ := decoded["choices"].([]any); len(choices) != 2 || choices[0] != "yes" {
		t.Fatalf("repeated values did not become an array: %v", decoded)
	}
	if decoded["multiple"] != true {
		t.Fatalf("boolean was not converted: %v", decoded)
	}
	if _, err := desktopMCPQueryArguments(map[string][]string{"x": {"left"}, "y": {"2"}}, "click_mouse"); err == nil {
		t.Fatal("a non-numeric coordinate must be reported")
	}
}

func TestEnsureDesktopMCPTokenIsStableAndPrivate(t *testing.T) {
	home := t.TempDir()
	token, err := EnsureDesktopMCPToken(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < 32 {
		t.Fatalf("token is too short: %q", token)
	}
	again, err := EnsureDesktopMCPToken(home)
	if err != nil || again != token {
		t.Fatalf("token changed between calls: %q %q %v", token, again, err)
	}
	info, err := os.Stat(filepath.Join(home, ".local", "share", "vmbox", "mcp-http-token"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode is %v", info.Mode().Perm())
	}
}

// The agent finds the façade by reading this file, so it has to carry both the
// address and the token the handler will demand.
func TestWriteDesktopMCPEndpointDescribesTheFacade(t *testing.T) {
	home := t.TempDir()
	if err := writeDesktopMCPEndpoint(home, "assignment", "secret-token"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(desktopMCPEndpointFile(home))
	if err != nil {
		t.Fatal(err)
	}
	endpoint := map[string]string{}
	if err := json.Unmarshal(data, &endpoint); err != nil {
		t.Fatal(err)
	}
	if endpoint["url"] != desktopMCPHTTPURL("assignment") || endpoint["promptUrl"] != desktopMCPHTTPURL("assignment")+"/prompt" || endpoint["token"] != "secret-token" {
		t.Fatalf("endpoint file is %v", endpoint)
	}
}

// Boxes on a shared worker collide on loopback unless each gets its own port.
func TestDesktopMCPPortIsPerAssignment(t *testing.T) {
	first, second := DesktopMCPPort("worker-a"), DesktopMCPPort("worker-b")
	if first == second {
		t.Fatalf("both assignments landed on %d", first)
	}
	if first != DesktopMCPPort("worker-a") {
		t.Fatal("the port moved between calls")
	}
	for _, port := range []int{first, second} {
		if port < 40_000 || port >= 50_000 {
			t.Fatalf("port %d is outside the façade range", port)
		}
	}
}

// The façade is not a Codex detail: every harness gets it, and a box with no
// managed desktop has no assignment to serve under.
func TestEnsureAgentBackendStartsTheFacadeForEveryAgent(t *testing.T) {
	originalFacade, originalCodex := EnsureDesktopMCPHTTP, EnsureCodexAppServer
	t.Cleanup(func() { EnsureDesktopMCPHTTP, EnsureCodexAppServer = originalFacade, originalCodex })
	EnsureCodexAppServer = func(context.Context, string) error { return nil }
	for _, agent := range []string{"codex", "claude", "opencode", "shell"} {
		started := ""
		EnsureDesktopMCPHTTP = func(_ context.Context, assignment string) error {
			started = assignment
			return nil
		}
		if err := ensureAgentBackend(context.Background(), "session", agent, "worker-a"); err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
		if started != "worker-a" {
			t.Fatalf("%s did not start the façade", agent)
		}
	}
	originalTmux := tmuxCommand
	t.Cleanup(func() { tmuxCommand = originalTmux })
	tmuxCommand = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("no façade should have been started")
		return nil, nil
	}
	if err := originalFacade(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	// An image without the box runtime cannot serve the façade; blocking the
	// agent launch on it for the ready timeout would be worse than not having it.
	t.Setenv("VMBOX_WORKSPACE_ROOT", t.TempDir())
	if err := originalFacade(context.Background(), "worker-a"); err != nil {
		t.Fatal(err)
	}
}

// A restored snapshot can leave the well-known facade session running a shell
// even though nothing is listening. The session name alone must not block a
// replacement server from starting.
func TestEnsureDesktopMCPHTTPReplacesAnUnreadyNamedSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("VMBOX_WORKSPACE_ROOT", root)
	runtime := desktopRuntimePath()
	if err := os.MkdirAll(filepath.Dir(runtime), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtime, []byte("runtime"), 0o755); err != nil {
		t.Fatal(err)
	}

	originalTmux, originalReady := tmuxCommand, desktopMCPHTTPReady
	t.Cleanup(func() {
		tmuxCommand = originalTmux
		desktopMCPHTTPReady = originalReady
	})
	readyChecks := 0
	desktopMCPHTTPReady = func(context.Context, string) bool {
		readyChecks++
		return readyChecks > 1
	}
	commands := []string{}
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		commands = append(commands, strings.Join(args, " "))
		return nil, nil // has-session reports the stale named session exists.
	}

	if err := EnsureDesktopMCPHTTP(context.Background(), "worker-a"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(commands, "\n")
	if !strings.Contains(joined, "kill-session -t ="+desktopMCPHTTPSession()) {
		t.Fatalf("stale facade session was not stopped:\n%s", joined)
	}
	if !strings.Contains(joined, "new-session -d -s "+desktopMCPHTTPSession()) {
		t.Fatalf("replacement facade session was not started:\n%s", joined)
	}
}

func stubTmuxSessions(t *testing.T, sessions map[string]string) {
	t.Helper()
	original := tmuxCommand
	t.Cleanup(func() { tmuxCommand = original })
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "list-sessions":
			names := make([]string, 0, len(sessions))
			for name := range sessions {
				names = append(names, name)
			}
			sort.Strings(names)
			return []byte(strings.Join(names, "\n") + "\n"), nil
		case "show-environment":
			agent, ok := sessions[args[2]]
			if !ok || agent == "" {
				return nil, fmt.Errorf("unknown variable")
			}
			return []byte(taskAgentEnvironment + "=" + agent + "\n"), nil
		}
		return nil, fmt.Errorf("unexpected tmux call %v", args)
	}
}

// The façade serves the whole box from one process, so a chat reply written
// from it would otherwise be filed under the façade's own tmux session, where
// the controller never looks.
func TestDesktopMCPHTTPNamesTheConversationForChatTools(t *testing.T) {
	stubTmuxSessions(t, map[string]string{
		"codex-abc123":                      "codex",
		"vmbox-internal-codex-codex-abc123": "",
		"vmbox-internal-mcp-http":           "",
		"shell-one":                         "shell",
	})
	session, err := soleAgentConversation(context.Background())
	if err != nil || session != "codex-abc123" {
		t.Fatalf("sole conversation = %q %v", session, err)
	}
}

func TestSoleAgentConversationRefusesToGuess(t *testing.T) {
	stubTmuxSessions(t, map[string]string{"codex-abc123": "codex", "claude-def456": "claude"})
	session, err := soleAgentConversation(context.Background())
	if err == nil {
		t.Fatalf("two conversations must not resolve to %q", session)
	}
	for _, want := range []string{"X-Vmbox-Session", "codex-abc123", "claude-def456"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the caller cannot choose from %q", err.Error())
		}
	}
	stubTmuxSessions(t, map[string]string{"shell-one": "shell"})
	if _, err = soleAgentConversation(context.Background()); err == nil {
		t.Fatal("a box with no agent conversation must say so")
	}
}

// A named session reaches writeChatEvent through the context, because the
// façade cannot set a process-wide environment variable per request.
func TestWithChatSessionOverridesTheHostingSession(t *testing.T) {
	t.Setenv("VMBOX_CHAT_SESSION", "codex-hosting")
	session, err := chatSession(WithChatSession(context.Background(), "claude-named"))
	if err != nil || session != "claude-named" {
		t.Fatalf("named session = %q %v", session, err)
	}
	if _, err = chatSession(WithChatSession(context.Background(), "not a session")); err == nil {
		t.Fatal("an invalid session name must be refused")
	}
	if session, err = chatSession(context.Background()); err != nil || session != "codex-hosting" {
		t.Fatalf("without a name the hosting session applies: %q %v", session, err)
	}
}

// vmbox_session steers the call; it is not a tool argument.
func TestDesktopMCPHTTPSessionParameterIsNotAToolArgument(t *testing.T) {
	stubTmuxSessions(t, map[string]string{"codex-abc123": "codex"})
	handler := desktopMCPHTTPHandler("assignment", "secret-token", allDesktopToolPolicy)
	status, body := desktopMCPHTTPRequest(t, handler, http.MethodGet, "/tools/click_mouse?x=1&y=2&vmbox_session=codex-abc123", "secret-token", "")
	if status == http.StatusBadRequest && strings.Contains(body["error"].(string), "unknown tool argument") {
		t.Fatalf("vmbox_session was treated as a tool argument: %v", body)
	}
}
