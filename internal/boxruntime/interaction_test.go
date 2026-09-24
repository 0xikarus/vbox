package boxruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDeliverTmuxInputReturnsSuccessForPersistedDelivery(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "messages")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "message_1.delivered"), []byte("done\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := DeliverTmuxInput(context.Background(), root, "vmbox", "message_1", "Y Z ä ö ü ß @ € | \\ [ ] { } ~", true); err != nil {
		t.Fatal(err)
	}
}

func TestDeliverTmuxInputRefusesAmbiguousReplay(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "messages")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "message_2.pending"), []byte("started\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := DeliverTmuxInput(context.Background(), root, "vmbox", "message_2", "do not replay", true)
	if !errors.Is(err, ErrAmbiguousMessage) {
		t.Fatalf("error=%v", err)
	}
}

func TestDeliverTmuxKeysUsesAllowlistedTmuxKeyEvents(t *testing.T) {
	originalCommand := tmuxCommand
	t.Cleanup(func() { tmuxCommand = originalCommand })
	var call []string
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if stdin != "" {
			t.Fatalf("key delivery wrote stdin %q", stdin)
		}
		call = append([]string(nil), args...)
		return nil, nil
	}
	root := t.TempDir()
	if err := DeliverTmuxKeys(context.Background(), root, "codex", "keys_1", []string{"Up", "Enter", "C-C"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"send-keys", "-t", "codex", "Up", "Enter", "C-C"}
	if strings.Join(call, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("tmux call=%q want=%q", call, want)
	}
	if _, err := os.Stat(filepath.Join(root, "messages", "keys_1.delivered")); err != nil {
		t.Fatalf("delivery marker: %v", err)
	}
}

func TestDeliverTmuxKeysRejectsArbitraryTmuxArguments(t *testing.T) {
	originalCommand := tmuxCommand
	t.Cleanup(func() { tmuxCommand = originalCommand })
	tmuxCommand = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		t.Fatal("unsupported key reached tmux")
		return nil, nil
	}
	if err := DeliverTmuxKeys(context.Background(), t.TempDir(), "codex", "keys_2", []string{"run-shell"}); err == nil {
		t.Fatal("arbitrary tmux argument was accepted")
	}
}

func TestDeliverTmuxInputWaitsAfterPasteBeforeSubmit(t *testing.T) {
	originalCommand, originalPause, originalConfirm := tmuxCommand, tmuxSubmitPause, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		tmuxCommand, tmuxSubmitPause, tmuxSubmitConfirmPause = originalCommand, originalPause, originalConfirm
	})
	paused := false
	submitted := false
	tmuxSubmitPause = func(context.Context) error {
		paused = true
		return nil
	}
	tmuxSubmitConfirmPause = func(context.Context) error { return nil }
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "send-keys" {
			t.Fatal("task submit used tmux's symbolic key instead of a terminal byte")
		}
		if len(args) > 0 && args[0] == "load-buffer" && stdin == "\r" {
			if !paused {
				t.Fatal("carriage return was loaded before the TUI paste pause")
			}
			submitted = true
		}
		if len(args) > 0 && args[0] == "capture-pane" {
			return []byte("────────────────\n❯\u00a0\n────────────────"), nil
		}
		return nil, nil
	}
	if err := DeliverTmuxInput(context.Background(), t.TempDir(), "claude", "message_submit", "hello", true); err != nil {
		t.Fatal(err)
	}
	if !paused || !submitted {
		t.Fatalf("pause=%v submitted=%v", paused, submitted)
	}
}

func TestDeliverTmuxInputAcceptsBashCarriageReturnWithoutTUIPrompt(t *testing.T) {
	originalCommand, originalPause := tmuxCommand, tmuxSubmitPause
	t.Cleanup(func() { tmuxCommand, tmuxSubmitPause = originalCommand, originalPause })
	tmuxSubmitPause = func(context.Context) error { return nil }
	carriageReturns := 0
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if args[0] == "display-message" {
			return []byte("bash\n"), nil
		}
		if args[0] == "capture-pane" {
			return []byte("worker$ printf hello\nhello\nworker$ "), nil
		}
		if args[0] == "load-buffer" && stdin == "\r" {
			carriageReturns++
		}
		return nil, nil
	}
	root := t.TempDir()
	if err := DeliverTmuxInput(context.Background(), root, "vmbox", "shell_message", "printf hello", true); err != nil {
		t.Fatal(err)
	}
	if carriageReturns != 1 {
		t.Fatalf("carriage returns=%d, want exactly one", carriageReturns)
	}
	if _, err := os.Stat(filepath.Join(root, "messages", "shell_message.delivered")); err != nil {
		t.Fatalf("shell command not recorded as delivered: %v", err)
	}
}

func TestDeliverTmuxInputRetriesOnlySubmitWhileClaudeInputIsStaged(t *testing.T) {
	originalCommand, originalPause, originalConfirm := tmuxCommand, tmuxSubmitPause, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		tmuxCommand, tmuxSubmitPause, tmuxSubmitConfirmPause = originalCommand, originalPause, originalConfirm
	})
	tmuxSubmitPause = func(context.Context) error { return nil }
	tmuxSubmitConfirmPause = func(context.Context) error { return nil }
	carriageReturns, captures := 0, 0
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if stdin == "hello" {
			return nil, nil
		}
		if stdin == "\r" {
			carriageReturns++
			return nil, nil
		}
		if len(args) > 0 && args[0] == "capture-pane" {
			captures++
			if captures == 2 {
				return []byte("Claude is repainting"), nil
			}
			if captures < 4 {
				return []byte("Claude Code v2\n────────────────\n❯\u00a0hello\n────────────────"), nil
			}
			return []byte("❯ hello\n● Working\n────────────────\n❯\u00a0\n────────────────"), nil
		}
		return nil, nil
	}
	if err := DeliverTmuxInput(context.Background(), t.TempDir(), "claude", "message_retry", "hello", true); err != nil {
		t.Fatal(err)
	}
	if carriageReturns != 2 {
		t.Fatalf("carriage returns=%d, want 2 without replaying prompt text", carriageReturns)
	}
}

func TestDeliverTmuxInputWaitsPastClaudeSuggestionForPastedText(t *testing.T) {
	originalCommand, originalPause, originalConfirm := tmuxCommand, tmuxSubmitPause, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		tmuxCommand, tmuxSubmitPause, tmuxSubmitConfirmPause = originalCommand, originalPause, originalConfirm
	})
	tmuxSubmitPause = func(context.Context) error { return nil }
	tmuxSubmitConfirmPause = func(context.Context) error { return nil }
	carriageReturns, captures := 0, 0
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if stdin == "\r" {
			carriageReturns++
			return nil, nil
		}
		if len(args) > 0 && args[0] == "capture-pane" {
			captures++
			switch {
			case captures < 3:
				return []byte("Claude Code v2\n❯\u00a0Try \"write a test\""), nil
			case captures == 3:
				return []byte("Claude Code v2\n❯\u00a0hello"), nil
			default:
				return []byte("Claude Code v2\n❯\u00a0"), nil
			}
		}
		return nil, nil
	}
	if err := DeliverTmuxInput(context.Background(), t.TempDir(), "claude", "message_delayed_paste", "hello", true); err != nil {
		t.Fatal(err)
	}
	if carriageReturns != 2 || captures < 7 {
		t.Fatalf("carriage returns=%d captures=%d; suggestion was mistaken for a completed delivery", carriageReturns, captures)
	}
}

func TestDeliverTmuxInputLeavesStagedTextBehindCodexUpdatePrompt(t *testing.T) {
	originalCommand, originalPause := tmuxCommand, tmuxSubmitPause
	t.Cleanup(func() { tmuxCommand, tmuxSubmitPause = originalCommand, originalPause })
	tmuxSubmitPause = func(context.Context) error { return nil }
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "capture-pane" {
			return []byte("Update available! 0.153.0 -> 0.153.2\n1. Update now\n2. Skip\n3. Skip until next version\nPress enter to continue"), nil
		}
		if stdin == "\r" || len(args) > 0 && args[0] == "send-keys" {
			t.Fatal("update prompt was submitted instead of surfaced to the controller")
		}
		return nil, nil
	}
	root := t.TempDir()
	if err := DeliverTmuxInput(context.Background(), root, "codex", "message_update", "hello", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "messages", "message_update.delivered")); err != nil {
		t.Fatalf("staged task was not recorded: %v", err)
	}
}

func TestTmuxInteractionRejectsUnsafeNames(t *testing.T) {
	for _, value := range []string{"", "../other", "name:window", "bad name", "ä"} {
		if err := validateTmuxToken("session", value); err == nil {
			t.Errorf("unsafe value %q accepted", value)
		}
	}
}

func TestStartTmuxTaskRejectsAnExistingSessionForAnotherAgent(t *testing.T) {
	original := tmuxCommand
	t.Cleanup(func() { tmuxCommand = original })
	var calls []string
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if len(args) > 0 && args[0] == "show-environment" {
			return []byte("VMBOX_TASK_AGENT=shell\n"), nil
		}
		return nil, nil
	}
	err := StartTmuxTask(context.Background(), t.TempDir(), "vmbox", "claude", "message_3", "hello")
	if err == nil || !strings.Contains(err.Error(), "choose a different session name") {
		t.Fatalf("error=%v calls=%v", err, calls)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "load-buffer") || strings.HasPrefix(call, "paste-buffer") {
			t.Fatalf("prompt was pasted into the wrong session: %v", calls)
		}
	}
}

func TestStartTmuxTaskWaitsForCodexInputBeforeDeliveringPrompt(t *testing.T) {
	stubCodexBackend(t)
	stubRegisteredAgent(t, "codex")
	originalCommand, originalInterval, originalTimeout, originalSettle, originalConfirm := tmuxCommand, agentReadyPollInterval, agentReadyTimeout, agentReadySettlePause, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		tmuxCommand, agentReadyPollInterval, agentReadyTimeout, agentReadySettlePause, tmuxSubmitConfirmPause = originalCommand, originalInterval, originalTimeout, originalSettle, originalConfirm
	})
	agentReadyPollInterval = 0
	agentReadyTimeout = time.Second
	agentReadySettlePause = func(context.Context) error { return nil }
	tmuxSubmitConfirmPause = func(context.Context) error { return nil }
	var calls []string
	captures := 0
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if strings.HasPrefix(call, "has-session") {
			return nil, errors.New("missing")
		}
		if strings.HasPrefix(call, "capture-pane") {
			captures++
			if captures == 1 {
				return []byte("starting"), nil
			}
			if captures == 2 {
				return []byte("OpenAI Codex\nApproaching rate limits\n› 1. Switch model\nPress enter to confirm or esc to go back"), nil
			}
			return []byte("OpenAI Codex\n› Ask Codex to do anything"), nil
		}
		return nil, nil
	}
	if err := StartTmuxTask(context.Background(), t.TempDir(), "codex-ready", "codex", "message_4", "hello"); err != nil {
		t.Fatal(err)
	}
	if captures < 2 {
		t.Fatalf("prompt was delivered before readiness: %v", calls)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "check_for_update_on_startup=false") {
		t.Fatalf("managed Codex session may intercept the initial prompt with an update menu: %v", calls)
	}
	joined := strings.Join(calls, "\n")
	dismissed, delivery := strings.Index(joined, "send-keys -t codex-ready Escape"), strings.Index(joined, "load-buffer")
	if dismissed < 0 || delivery < 0 || dismissed >= delivery {
		t.Fatalf("rate limit reminder was not dismissed before delivery: %v", calls)
	}
}

func TestCodexUpdateMenuIsNotInputReady(t *testing.T) {
	content := "OpenAI Codex\n› Ask Codex to do anything\nUpdate available!\nSkip until next version\nPress enter to continue"
	if agentInputReady("codex", content) {
		t.Fatal("update menu was treated as ready for a task prompt")
	}
}

func TestCodexLongConversationRemainsInputReadyWithoutStartupHeader(t *testing.T) {
	content := "• Worked for 9m 33s · done\n\n› Ask Codex to do anything\n\n  gpt-6-sol high · /data/workspace · Add map displacement and zoom\n"
	if !agentInputReady("codex", content) {
		t.Fatal("long Codex conversation lost its Chat input readiness")
	}
	if agentInputReady("codex", "› Ask Codex to do anything\n• Running a tool\n└ still busy\n") {
		t.Fatal("stale composer above active output was treated as ready")
	}
	// The readiness probe may see a draft; ensureCodexEmptyComposer rejects it
	// before typing any Chat text into that user's unsent input.
}

func TestStartTmuxTaskAcceptsClaudeTrustBeforeDeliveringPrompt(t *testing.T) {
	stubRegisteredAgent(t, "claude")
	originalCommand, originalInterval, originalTimeout, originalSettle, originalConfirm := tmuxCommand, agentReadyPollInterval, agentReadyTimeout, agentReadySettlePause, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		tmuxCommand, agentReadyPollInterval, agentReadyTimeout, agentReadySettlePause, tmuxSubmitConfirmPause = originalCommand, originalInterval, originalTimeout, originalSettle, originalConfirm
	})
	agentReadyPollInterval = 0
	agentReadyTimeout = time.Second
	agentReadySettlePause = func(context.Context) error { return nil }
	tmuxSubmitConfirmPause = func(context.Context) error { return nil }
	var calls []string
	captures := 0
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if strings.HasPrefix(call, "has-session") {
			return nil, errors.New("missing")
		}
		if strings.HasPrefix(call, "capture-pane") {
			captures++
			if captures == 1 {
				return []byte("WARNING: Loading development channels\n❯ 1. I am using this for local development\n  2. Exit\nEnter to confirm · Esc to cancel"), nil
			}
			if captures == 2 {
				return []byte("Quick safety check:\n❯ No, exit\n  Yes, I trust this folder\nEnter to confirm"), nil
			}
			if captures == 3 || captures == 4 {
				return []byte("Claude Code v2\n❯\u00a0Try \"write a test for <filepath>\""), nil
			}
			if captures == 5 {
				return []byte("Claude Code v2\n❯\u00a0hello"), nil
			}
			return []byte("Claude Code v2\n❯\u00a0"), nil
		}
		return nil, nil
	}
	if err := StartTmuxTask(context.Background(), t.TempDir(), "claude-ready", "claude", "message_5", "hello"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "new-session -d -s claude-ready -c /data/workspace -- env DISABLE_AUTOUPDATER=1 claude --add-dir /data/home/.local/share/vmbox/chat --dangerously-load-development-channels server:vmbox-desktop") {
		t.Fatalf("managed Claude task did not disable background self-update: %v", calls)
	}
	channel := strings.Index(joined, "send-keys -t claude-ready Enter")
	trustDown := strings.Index(joined, "send-keys -t claude-ready Down")
	trust := -1
	if trustDown >= 0 {
		if offset := strings.Index(joined[trustDown:], "send-keys -t claude-ready Enter"); offset >= 0 {
			trust = trustDown + offset
		}
	}
	delivery := strings.Index(joined, "load-buffer")
	if channel < 0 || trustDown < channel || trust < trustDown || delivery < 0 || trust >= delivery {
		t.Fatalf("channel and workspace confirmations were not accepted before prompt delivery: %v", calls)
	}
}

func TestStartTmuxTaskCanStartAgentWithoutTerminalPrompt(t *testing.T) {
	stubRegisteredAgent(t, "claude")
	originalCommand, originalSettle := tmuxCommand, agentReadySettlePause
	t.Cleanup(func() { tmuxCommand, agentReadySettlePause = originalCommand, originalSettle })
	agentReadySettlePause = func(context.Context) error { return nil }
	var calls []string
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if stdin != "" || len(args) > 0 && (args[0] == "load-buffer" || args[0] == "paste-buffer") {
			t.Fatal("agent startup attempted terminal prompt delivery")
		}
		if len(args) > 0 && args[0] == "has-session" {
			return nil, errors.New("missing")
		}
		if len(args) > 0 && args[0] == "capture-pane" {
			return []byte("Claude Code v2\n❯\u00a0Try \"write a test\""), nil
		}
		return nil, nil
	}
	if err := StartTmuxTask(context.Background(), t.TempDir(), "claude-native", "claude", "message-native", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "new-session -d -s claude-native") {
		t.Fatalf("agent session was not started: %v", calls)
	}
}

func TestStartOpenCodeChatStartsBareAndSubmitsStructuredPrompt(t *testing.T) {
	stubRegisteredAgent(t, "opencode")
	home := t.TempDir()
	t.Setenv("HOME", home)
	originalCommand, originalProbe, originalSettle, originalVisible, originalTransport := tmuxCommand, openCodeReadyProbe, agentReadySettlePause, openCodeVisibleClient, http.DefaultTransport
	t.Cleanup(func() {
		tmuxCommand, openCodeReadyProbe, agentReadySettlePause, openCodeVisibleClient, http.DefaultTransport = originalCommand, originalProbe, originalSettle, originalVisible, originalTransport
	})
	openCodeReadyProbe = func(context.Context, string) (bool, error) { return true, nil }
	agentReadySettlePause = func(context.Context) error { return nil }
	openCodeVisibleClient = func(context.Context, string, string) (*http.Client, error) { return &http.Client{}, nil }
	var encoded bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.RGBA{R: 80, G: 20, B: 180, A: 255})
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	submitted := false
	http.DefaultTransport = openCodeTestTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/prompt" || request.Method != http.MethodPost {
			t.Fatalf("unexpected OpenCode request: %s %s", request.Method, request.URL.Path)
		}
		var payload struct {
			Parts []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				MIME     string `json:"mime"`
				Filename string `json:"filename"`
				URL      string `json:"url"`
			} `json:"parts"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Parts) != 2 || payload.Parts[0].Type != "text" || payload.Parts[0].Text != "inspect this" {
			t.Fatalf("wrong OpenCode prompt parts: %+v", payload.Parts)
		}
		file := payload.Parts[1]
		if file.Type != "file" || file.MIME != "image/png" || file.Filename != "image-1.png" || !strings.HasPrefix(file.URL, "data:image/png;base64,") {
			t.Fatalf("wrong OpenCode image part: %+v", file)
		}
		submitted = true
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"sessionID":"session-1"}`)), Header: make(http.Header)}, nil
	})
	var calls []string
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, "\n"))
		if stdin != "" || len(args) > 0 && (args[0] == "load-buffer" || args[0] == "paste-buffer") {
			t.Fatal("OpenCode startup used terminal input")
		}
		if len(args) > 0 && args[0] == "has-session" {
			return nil, errors.New("missing")
		}
		return nil, nil
	}
	inbound := ChatInbound{ID: "message-start", Text: "inspect this", Images: []ChatEventImage{{Name: "purple.png", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(encoded.Bytes())}}}
	if err := StartOpenCodeChat(context.Background(), t.TempDir(), home, "opencode-start", inbound); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "opencode\n--auto\n--hostname\n127.0.0.1") || strings.Contains(joined, "--prompt") {
		t.Fatalf("OpenCode startup arguments were incomplete: %v", calls)
	}
	if !submitted {
		t.Fatal("OpenCode initial prompt was not submitted through the visible bridge")
	}
}

type openCodeTestTransport func(*http.Request) (*http.Response, error)

func (transport openCodeTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestStartTmuxTaskDeliversToExistingOpenCodeWithoutRestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	originalVisible := openCodeVisibleClient
	t.Cleanup(func() { openCodeVisibleClient = originalVisible })
	openCodeVisibleClient = func(context.Context, string, string) (*http.Client, error) { return &http.Client{}, nil }
	originalCommand, originalProbe, originalSettle, originalTransport := tmuxCommand, openCodeReadyProbe, agentReadySettlePause, http.DefaultTransport
	t.Cleanup(func() {
		tmuxCommand, openCodeReadyProbe, agentReadySettlePause, http.DefaultTransport = originalCommand, originalProbe, originalSettle, originalTransport
	})
	openCodeReadyProbe = func(context.Context, string) (bool, error) { return true, nil }
	agentReadySettlePause = func(context.Context) error { return nil }
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if stdin != "" {
			t.Fatal("unexpected terminal input")
		}
		switch args[0] {
		case "has-session":
			return nil, nil
		case "show-environment":
			return []byte(taskAgentEnvironment + "=opencode"), nil
		default:
			t.Fatalf("existing session was modified: %v", args)
			return nil, nil
		}
	}
	submitted := 0
	http.DefaultTransport = openCodeTestTransport(func(request *http.Request) (*http.Response, error) {
		body := "{}"
		switch request.URL.Path {
		case "/prompt":
			if request.Method != http.MethodPost {
				t.Fatal("expected native prompt submission")
			}
			var payload struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Parts) != 1 || payload.Parts[0].Text != "continue existing work" {
				t.Fatalf("wrong prompt: %+v", payload)
			}
			submitted++
		default:
			t.Fatalf("unexpected API path %q", request.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	if err := StartTmuxTask(context.Background(), t.TempDir(), "opencode-existing", "opencode", "message-existing", "continue existing work"); err != nil {
		t.Fatal(err)
	}
	if submitted != 1 {
		t.Fatalf("native prompt submissions = %d", submitted)
	}
}

func TestResetAgentContextUsesHarnessCommandInExistingTUI(t *testing.T) {
	originalCommand, originalOpenCodeProbe, originalCodexProbe, originalPause := tmuxCommand, openCodeReadyProbe, CodexAppServerReady, tmuxSubmitPause
	t.Cleanup(func() {
		tmuxCommand, openCodeReadyProbe, CodexAppServerReady, tmuxSubmitPause = originalCommand, originalOpenCodeProbe, originalCodexProbe, originalPause
	})
	openCodeReadyProbe = func(context.Context, string) (bool, error) { return true, nil }
	CodexAppServerReady = func(context.Context, string) (bool, error) { return true, nil }
	tmuxSubmitPause = func(context.Context) error { return nil }
	for _, test := range []struct {
		agent string
		want  []string
	}{
		{agent: "codex", want: []string{"respawn"}},
		{agent: "opencode", want: []string{"/new", "\r"}},
	} {
		t.Run(test.agent, func(t *testing.T) {
			var inputs []string
			tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
				if len(args) > 0 && args[0] == "capture-pane" {
					if test.agent == "codex" {
						return []byte("OpenAI Codex (v0.155.1)\n› Ask Codex to do anything"), nil
					}
					return nil, nil
				}
				if len(args) > 0 && args[0] == "respawn-pane" {
					if !strings.Contains(strings.Join(args, " "), "--remote") || strings.Contains(strings.Join(args, " "), "fresh-thread") {
						t.Fatalf("fresh TUI did not start without a stale thread ID: %v", args)
					}
					inputs = append(inputs, "respawn")
				}
				if len(args) > 0 && args[0] == "load-buffer" {
					inputs = append(inputs, stdin)
				}
				if len(args) > 0 && args[0] == "send-keys" {
					inputs = append(inputs, args[len(args)-1])
				}
				return nil, nil
			}
			root := t.TempDir()
			if err := ResetAgentContext(context.Background(), root, test.agent+"-session", test.agent, "reset-message"); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(inputs, test.want) {
				t.Fatalf("terminal inputs = %q", inputs)
			}
			if err := ResetAgentContext(context.Background(), root, test.agent+"-session", test.agent, "reset-message"); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(inputs, test.want) {
				t.Fatalf("idempotent retry replayed terminal input: %q", inputs)
			}
			if test.agent == "codex" {
				path := filepath.Join(root, "chat", "codex-reset-threads-v2", "reset-message")
				if data, err := os.ReadFile(path); err != nil || strings.TrimSpace(string(data)) != "visible-tui" {
					t.Fatalf("reset marker was not saved: %q %v", data, err)
				}
				if data, err := os.ReadFile(codexResetPendingFile(root, test.agent+"-session")); err != nil || strings.TrimSpace(string(data)) != "visible-tui" {
					t.Fatalf("visible thread was not remembered: %q %v", data, err)
				}
			}
		})
	}
	if err := ResetAgentContext(context.Background(), t.TempDir(), "shell-session", "shell", "reset-message"); err == nil {
		t.Fatal("shell context reset accepted")
	}
}

func TestResetClaudeContextRespawnsChannelTUI(t *testing.T) {
	workspaceRoot := t.TempDir()
	t.Setenv("VMBOX_WORKSPACE_ROOT", workspaceRoot)
	settingsPath := filepath.Join(workspaceRoot, "home", ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"permissions":{"defaultMode":"auto"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	originalCommand, originalSettle, originalChannelWait := tmuxCommand, agentReadySettlePause, claudeChannelReadyWait
	t.Cleanup(func() {
		tmuxCommand, agentReadySettlePause, claudeChannelReadyWait = originalCommand, originalSettle, originalChannelWait
	})
	agentReadySettlePause = func(context.Context) error { return nil }
	var calls []string
	channelWaits := 0
	claudeChannelReadyWait = func(_ context.Context, session string, prior map[string]struct{}) error {
		calls = append(calls, "wait-channel "+session)
		channelWaits++
		if session != "claude-session" {
			t.Fatalf("waited for the wrong Claude channel %q", session)
		}
		if len(prior) != 0 {
			t.Fatalf("unexpected prior channels: %v", prior)
		}
		return nil
	}
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if len(args) > 0 && args[0] == "capture-pane" {
			return []byte("Claude Code v2.1.276\n❯ Try \"fix a bug\""), nil
		}
		return nil, nil
	}
	root := t.TempDir()
	if err := ResetAgentContext(context.Background(), root, "claude-session", "claude", "reset-message"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "respawn-pane -k -t claude-session") {
		t.Fatalf("Claude context was not replaced with a fresh channel TUI: %v", calls)
	}
	if strings.Contains(joined, "load-buffer") || strings.Contains(joined, "paste-buffer") {
		t.Fatalf("Claude reset was injected as terminal text: %v", calls)
	}
	if channelWaits != 1 {
		t.Fatalf("fresh Claude channel was not awaited: %d", channelWaits)
	}
	promptReady, channelReady := strings.Index(joined, "capture-pane"), strings.Index(joined, "wait-channel claude-session")
	if promptReady < 0 || channelReady < 0 || promptReady > channelReady {
		t.Fatalf("Claude channel was awaited before startup dialogs could be handled: %v", calls)
	}
	settingsData, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Permissions struct {
			DefaultMode string `json:"defaultMode"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(settingsData, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Permissions.DefaultMode != "bypassPermissions" {
		t.Fatalf("Claude reset retained unsafe default mode %q", settings.Permissions.DefaultMode)
	}
	before := len(calls)
	if err := ResetAgentContext(context.Background(), root, "claude-session", "claude", "reset-message"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != before {
		t.Fatalf("idempotent retry restarted Claude again: %v", calls[before:])
	}
}

func stubRegisteredAgent(t *testing.T, agent string) {
	t.Helper()
	home, bin := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, agent), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
}

func TestClaudeInputReadinessRejectsBareStartupPrompt(t *testing.T) {
	if agentInputReady("claude", "Claude Code v2.1.259\n❯") {
		t.Fatal("bare Claude startup prompt was accepted before the real input placeholder")
	}
	if !agentInputReady("claude", "Claude Code v2.1.259\n❯\u00a0Try \"fix lint errors\"") {
		t.Fatal("Claude real input placeholder was not recognized")
	}
}

func TestWaitForAgentReadyUsesOpenCodeSessionAPI(t *testing.T) {
	originalProbe, originalInterval, originalTimeout := openCodeReadyProbe, agentReadyPollInterval, agentReadyTimeout
	t.Cleanup(func() {
		openCodeReadyProbe, agentReadyPollInterval, agentReadyTimeout = originalProbe, originalInterval, originalTimeout
	})
	agentReadyPollInterval = 0
	agentReadyTimeout = time.Second
	attempts := 0
	openCodeReadyProbe = func(_ context.Context, session string) (bool, error) {
		if session != "opencode-api" {
			t.Fatalf("session=%q", session)
		}
		attempts++
		return attempts == 2, nil
	}
	if err := waitForAgentReady(context.Background(), "opencode-api", "opencode"); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d", attempts)
	}
}

func TestCaptureTmuxScreenRejectsMissingOrWrongSessionMetadata(t *testing.T) {
	original := captureTmuxCommand
	t.Cleanup(func() { captureTmuxCommand = original })
	for _, metadata := range []string{
		"\x1f\x1f\x1f\x1f\x1f\n",
		"other-session\x1f%1\x1ftitle\x1fclaude\x1f80\x1f24\n",
	} {
		captureCalls := 0
		captureTmuxCommand = func(_ context.Context, args ...string) ([]byte, error) {
			captureCalls++
			return []byte(metadata), nil
		}
		if _, err := CaptureTmuxScreen(context.Background(), "expected-session", 100); err == nil || !strings.Contains(err.Error(), "requested session") {
			t.Fatalf("metadata=%q error=%v", metadata, err)
		}
		if captureCalls != 1 {
			t.Fatalf("wrong pane was captured after mismatched metadata: calls=%d", captureCalls)
		}
	}
}

func TestSteerInterruptsBeforePasteAndDoesNotReplay(t *testing.T) {
	originalCommand, originalPause := tmuxCommand, tmuxSubmitPause
	t.Cleanup(func() { tmuxCommand = originalCommand; tmuxSubmitPause = originalPause })
	tmuxSubmitPause = func(context.Context) error { return nil }
	var calls []string
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "show-environment" {
			return []byte(taskAgentEnvironment + "=codex"), nil
		}
		return nil, nil
	}
	root := t.TempDir()
	if err := DeliverTmuxInput(context.Background(), root, "agent", "steer_1", "change direction", false, true); err != nil {
		t.Fatal(err)
	}
	interrupt, paste := -1, -1
	for i, call := range calls {
		if strings.HasPrefix(call, "send-keys") {
			interrupt = i
		}
		if strings.HasPrefix(call, "paste-buffer") {
			paste = i
		}
	}
	if interrupt < 0 || paste < interrupt {
		t.Fatal("steer did not interrupt before pasting")
	}
	n := len(calls)
	if err := DeliverTmuxInput(context.Background(), root, "agent", "steer_1", "change direction", false, true); err != nil || len(calls) != n {
		t.Fatal("idempotent steer replayed")
	}
}

// Claude 2.1.276 added a permission-mode dialog that blocks the prompt. Nobody
// is at the box to answer it, and the box is deliberately on bypassPermissions,
// so the second choice is the one that keeps its configuration.
func TestStartupDialogKeepsBypassPermissions(t *testing.T) {
	pane := "Make auto mode your default permission mode?\n" +
		"  Auto mode lets Claude handle permission prompts automatically.\n" +
		"  ❯ Yes, set auto mode as my default permission mode\n" +
		"    No, keep bypass permissions\n"
	dialog, waiting := pendingStartupDialog("claude", pane)
	if !waiting {
		t.Fatal("the auto mode dialog was not recognised")
	}
	if strings.Join(dialog.keys, " ") != "Down Enter" {
		t.Fatalf("auto mode would have been accepted: %v", dialog.keys)
	}
	if _, waiting = pendingStartupDialog("codex", pane); waiting {
		t.Fatal("a claude dialog must not be answered in a codex pane")
	}
}

func TestStartupDialogsCoverEachKnownPrompt(t *testing.T) {
	for _, testCase := range []struct{ agent, pane, keys string }{
		{"claude", "WARNING: Loading development channels\n❯ 1. I am using this for local development\n  2. Exit\nEnter to confirm", "Enter"},
		{"claude", "Quick safety check:\n❯ No, exit\n  Yes, I trust this folder\nEnter to confirm", "Down Enter"},
		{"codex", "OpenAI Codex\nApproaching rate limits\n› 1. Switch model\nPress enter to confirm or esc to go back", "Escape"},
	} {
		dialog, waiting := pendingStartupDialog(testCase.agent, testCase.pane)
		if !waiting || strings.Join(dialog.keys, " ") != testCase.keys {
			t.Fatalf("%s pane answered with %v, want %q", testCase.agent, dialog.keys, testCase.keys)
		}
	}
	if _, waiting := pendingStartupDialog("claude", "Claude Code v2\n❯ Try \"write a test\""); waiting {
		t.Fatal("a ready prompt must not be mistaken for a dialog")
	}
}

func TestWaitForAgentReadyPausesBetweenDialogKeys(t *testing.T) {
	originalCommand, originalInterval, originalTimeout, originalPause := tmuxCommand, agentReadyPollInterval, agentReadyTimeout, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		tmuxCommand, agentReadyPollInterval, agentReadyTimeout, tmuxSubmitConfirmPause = originalCommand, originalInterval, originalTimeout, originalPause
	})
	agentReadyPollInterval = 0
	agentReadyTimeout = time.Second
	var calls []string
	captures := 0
	tmuxSubmitConfirmPause = func(context.Context) error {
		calls = append(calls, "pause")
		return nil
	}
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if strings.HasPrefix(call, "capture-pane") {
			captures++
			if captures == 1 {
				return []byte("Claude Code v2.1.276\nMake auto mode your default permission mode?\n❯ Yes, set auto mode as my default permission mode\n  No, keep bypass permissions"), nil
			}
			return []byte("Claude Code v2.1.276\n❯ Try \"fix a bug\""), nil
		}
		return nil, nil
	}
	if err := waitForAgentReady(context.Background(), "claude-sequential", "claude"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"capture-pane -p -J -S -80 -t claude-sequential",
		"send-keys -t claude-sequential Down",
		"pause",
		"send-keys -t claude-sequential Enter",
		"capture-pane -p -J -S -80 -t claude-sequential",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("dialog key sequence = %v, want %v", calls, want)
	}
}

// The dialog is answered before the prompt is delivered, or the message lands in
// a menu instead of the conversation.
func TestStartTmuxTaskAnswersAutoModeBeforeDeliveringPrompt(t *testing.T) {
	stubRegisteredAgent(t, "claude")
	originalCommand, originalInterval, originalTimeout, originalSettle, originalConfirm := tmuxCommand, agentReadyPollInterval, agentReadyTimeout, agentReadySettlePause, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		tmuxCommand, agentReadyPollInterval, agentReadyTimeout, agentReadySettlePause, tmuxSubmitConfirmPause = originalCommand, originalInterval, originalTimeout, originalSettle, originalConfirm
	})
	agentReadyPollInterval = 0
	agentReadyTimeout = time.Second
	agentReadySettlePause = func(context.Context) error { return nil }
	tmuxSubmitConfirmPause = func(context.Context) error { return nil }
	var calls []string
	captures := 0
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if strings.HasPrefix(call, "has-session") {
			return nil, errors.New("missing")
		}
		if strings.HasPrefix(call, "capture-pane") {
			captures++
			if captures == 1 {
				return []byte("Claude Code v2.1.276\nMake auto mode your default permission mode?\n❯ Yes, set auto mode as my default permission mode\n  No, keep bypass permissions"), nil
			}
			if captures == 2 || captures == 3 {
				return []byte("Claude Code v2\n❯\u00a0Try \"write a test for <filepath>\""), nil
			}
			if captures == 4 {
				return []byte("Claude Code v2\n❯\u00a0hello"), nil
			}
			return []byte("Claude Code v2\n❯\u00a0"), nil
		}
		return nil, nil
	}
	if err := StartTmuxTask(context.Background(), t.TempDir(), "claude-auto", "claude", "message_9", "hello"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	down := strings.Index(joined, "send-keys -t claude-auto Down")
	answer := strings.Index(joined, "send-keys -t claude-auto Enter")
	delivery := strings.Index(joined, "load-buffer")
	if down < 0 || answer < 0 || down >= answer {
		t.Fatalf("the auto mode dialog was never answered: %v", calls)
	}
	if delivery < 0 || answer >= delivery {
		t.Fatalf("the prompt was delivered into the dialog: %v", calls)
	}
}

func TestStartTmuxTaskAnswersAutoModeThatAppearsAfterFirstReadyPrompt(t *testing.T) {
	stubRegisteredAgent(t, "claude")
	originalCommand, originalInterval, originalTimeout, originalSettle, originalConfirm := tmuxCommand, agentReadyPollInterval, agentReadyTimeout, agentReadySettlePause, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		tmuxCommand, agentReadyPollInterval, agentReadyTimeout, agentReadySettlePause, tmuxSubmitConfirmPause = originalCommand, originalInterval, originalTimeout, originalSettle, originalConfirm
	})
	agentReadyPollInterval = 0
	agentReadyTimeout = time.Second
	agentReadySettlePause = func(context.Context) error { return nil }
	tmuxSubmitConfirmPause = func(context.Context) error { return nil }
	var calls []string
	firstReady, selected, answered, delivered := false, false, false, false
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch {
		case strings.HasPrefix(call, "has-session"):
			return nil, errors.New("missing")
		case strings.HasPrefix(call, "send-keys -t claude-delayed Down"):
			selected = true
			return nil, nil
		case strings.HasPrefix(call, "send-keys -t claude-delayed Enter"):
			if !selected {
				t.Fatal("confirmed the default choice before selecting bypass permissions")
			}
			answered = true
			return nil, nil
		case strings.HasPrefix(call, "load-buffer"):
			delivered = true
			return nil, nil
		case strings.HasPrefix(call, "capture-pane"):
			if !firstReady {
				firstReady = true
				return []byte("Claude Code v2.1.276\n❯ Try \"fix a bug\""), nil
			}
			if !answered {
				return []byte("Claude Code v2.1.276\nMake auto mode your default permission mode?\n❯ Yes, set auto mode as my default permission mode\n  No, keep bypass permissions"), nil
			}
			if delivered {
				return []byte("Claude Code v2.1.276\n❯"), nil
			}
			return []byte("Claude Code v2.1.276\n❯ Try \"fix a bug\""), nil
		}
		return nil, nil
	}
	if err := StartTmuxTask(context.Background(), t.TempDir(), "claude-delayed", "claude", "message_delayed", "hello"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	down := strings.Index(joined, "send-keys -t claude-delayed Down")
	answer := strings.Index(joined, "send-keys -t claude-delayed Enter")
	delivery := strings.Index(joined, "load-buffer")
	if down < 0 || answer < 0 || down >= answer || delivery < 0 || answer >= delivery {
		t.Fatalf("delayed auto-mode dialog was not answered before delivery: %v", calls)
	}
}
