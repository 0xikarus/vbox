package boxruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	handler := desktopMCPHTTPHandler("assignment", "secret-token")
	for _, target := range []string{"/", "/tools", "/tools/desktop_screenshot"} {
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

// Readiness must answer before a caller has the token, so the start-up probe
// can tell "not listening yet" from "listening".
func TestDesktopMCPHTTPHealthIsOpen(t *testing.T) {
	status, body := desktopMCPHTTPRequest(t, desktopMCPHTTPHandler("assignment", "secret-token"), http.MethodGet, "/health", "", "")
	if status != http.StatusOK || body["ok"] != true {
		t.Fatalf("health returned %d %v", status, body)
	}
}

func TestDesktopMCPHTTPListsTheSameToolsAsMCP(t *testing.T) {
	status, body := desktopMCPHTTPRequest(t, desktopMCPHTTPHandler("assignment", "secret-token"), http.MethodGet, "/tools", "secret-token", "")
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
}

func TestDesktopMCPHTTPRejectsUnknownToolsAndArguments(t *testing.T) {
	handler := desktopMCPHTTPHandler("assignment", "secret-token")
	if status, _ := desktopMCPHTTPRequest(t, handler, http.MethodPost, "/tools/desktop_launch_missiles", "secret-token", `{}`); status != http.StatusBadRequest {
		t.Fatalf("unknown tool returned %d", status)
	}
	if status, _ := desktopMCPHTTPRequest(t, handler, http.MethodGet, "/tools/desktop_click?z=1", "secret-token", ""); status != http.StatusBadRequest {
		t.Fatalf("unknown argument returned %d", status)
	}
	status, body := desktopMCPHTTPRequest(t, handler, http.MethodPost, "/tools/desktop_click", "secret-token", `{"x":10}`)
	if status != http.StatusBadRequest || !strings.Contains(body["error"].(string), "y") {
		t.Fatalf("missing argument returned %d %v", status, body)
	}
	if status, _ := desktopMCPHTTPRequest(t, handler, http.MethodDelete, "/tools/desktop_click", "secret-token", ""); status != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE returned %d", status)
	}
}

// A shell script writes query strings far more easily than JSON, so GET values
// are converted to the types each tool's schema asks for.
func TestDesktopMCPHTTPQueryArgumentsFollowTheSchema(t *testing.T) {
	raw, err := desktopMCPQueryArguments(map[string][]string{"x": {"10"}, "y": {"20"}, "button": {"3"}}, "desktop_click")
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
	if _, err := desktopMCPQueryArguments(map[string][]string{"x": {"left"}, "y": {"2"}}, "desktop_click"); err == nil {
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
	if endpoint["url"] != desktopMCPHTTPURL("assignment") || endpoint["token"] != "secret-token" {
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
