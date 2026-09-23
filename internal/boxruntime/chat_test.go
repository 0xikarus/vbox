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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatMessageToolPersistsTextAndImageForController(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "codex-chat")
	var encoded bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.RGBA{R: 80, G: 20, B: 180, A: 255})
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "reply.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"replyTo": "message-1", "text": "purple image", "files": []string{path}})
	result, err := callDesktopTool(context.Background(), "assignment", "chat_message", args)
	if err != nil || result["isError"] == true {
		t.Fatalf("chat_message failed: %v %+v", err, result)
	}
	event, found, err := PullChatEvent(home, "codex-chat")
	if err != nil || !found {
		t.Fatalf("event unavailable: %v", err)
	}
	if event.Kind != "reply" || event.ReplyTo != "message-1" || event.Text != "purple image" || len(event.Images) != 1 || event.Images[0].MediaType != "image/png" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if err := AckChatEvent(home, "codex-chat", event.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := PullChatEvent(home, "codex-chat"); err != nil || found {
		t.Fatalf("acknowledged event remained: found=%t err=%v", found, err)
	}
}

func TestChatAskPersistsMultipleChoiceQuestion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "claude-chat")
	args := json.RawMessage(`{"replyTo":"message-2","question":"Pick colors","choices":["purple","green"],"multiple":true}`)
	if _, err := callDesktopTool(context.Background(), "assignment", "chat_ask", args); err != nil {
		t.Fatal(err)
	}
	event, found, err := PullChatEvent(home, "claude-chat")
	if err != nil || !found || event.Kind != "question" || event.Question == nil || !event.Question.Multiple || len(event.Question.Choices) != 2 {
		t.Fatalf("unexpected question: found=%t err=%v event=%+v", found, err, event)
	}
}

func TestSetBusyReportsTheHostingChatSession(t *testing.T) {
	original := DesktopSetBusy
	t.Cleanup(func() { DesktopSetBusy = original })
	t.Setenv("VMBOX_CHAT_SESSION", "codex-chat")
	var assignment, session string
	var busy bool
	DesktopSetBusy = func(_ context.Context, gotAssignment, gotSession string, gotBusy bool) error {
		assignment, session, busy = gotAssignment, gotSession, gotBusy
		return nil
	}
	result, err := callDesktopTool(context.Background(), "assignment-1", "set_busy", json.RawMessage(`{"busy":true}`))
	if err != nil || result["isError"] == true {
		t.Fatalf("set_busy failed: %v %+v", err, result)
	}
	if assignment != "assignment-1" || session != "codex-chat" || !busy {
		t.Fatalf("activity report lost its scope: assignment=%q session=%q busy=%t", assignment, session, busy)
	}
	if _, err := callDesktopTool(context.Background(), "assignment-1", "set_busy", json.RawMessage(`{"busy":null}`)); err == nil {
		t.Fatal("set_busy accepted a missing boolean")
	}
}

func TestChatSessionFindsSanitizedMCPThroughProcessTree(t *testing.T) {
	original := tmuxCommand
	t.Cleanup(func() { tmuxCommand = original })
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "list-panes" {
			return []byte("220\tmanaged-codex\n410\tother\n"), nil
		}
		t.Fatalf("unexpected tmux command: %v", args)
		return nil, nil
	}
	parents := map[int]int{500: 400, 400: 220, 220: 1}
	session, err := chatSessionFromProcessTree(context.Background(), 500, func(pid int) (int, error) {
		return parents[pid], nil
	})
	if err != nil || session != "managed-codex" {
		t.Fatalf("session=%q err=%v", session, err)
	}
}

func TestDeliverCodexChatQueuesEmbeddedImagesForVisibleThread(t *testing.T) {
	stubCodexDeliveryHealth(t)
	originalQueue := CodexQueueMessage
	t.Cleanup(func() { CodexQueueMessage = originalQueue })
	home, root := t.TempDir(), t.TempDir()
	var encoded bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.RGBA{R: 80, G: 20, B: 180, A: 255})
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	inbound := ChatInbound{ID: "message-1", Text: "Inspect [Image 1]", Images: []ChatEventImage{{Name: "purple.png", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(encoded.Bytes())}}}
	var queuedID, queuedText string
	var queuedPaths []string
	CodexQueueMessage = func(_ context.Context, _, _, _, id, text string, paths []string) error {
		queuedID, queuedText, queuedPaths = id, text, append([]string(nil), paths...)
		return nil
	}
	if err := DeliverCodexChat(context.Background(), root, home, "codex-chat", inbound); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(home, ".local/share/vmbox/chat/inbox/codex-chat/files/message-1/image-1.png")
	if queuedID != inbound.ID || queuedText != inbound.Text || len(queuedPaths) != 1 || queuedPaths[0] != wantPath {
		t.Fatalf("queued ID=%q text=%q paths=%v", queuedID, queuedText, queuedPaths)
	}
	if _, err := os.Stat(filepath.Join(home, ".local/share/vmbox/chat/inbox/codex-chat/message-1.json")); !os.IsNotExist(err) {
		t.Fatalf("delivered inbox retained: %v", err)
	}
}

func TestDeliverCodexChatQueuesLongPromptWithoutTerminalTyping(t *testing.T) {
	stubCodexDeliveryHealth(t)
	originalQueue := CodexQueueMessage
	t.Cleanup(func() { CodexQueueMessage = originalQueue })
	want := strings.Repeat("ab🙂", 16_000)
	var queued string
	CodexQueueMessage = func(_ context.Context, _, _, _, _, text string, _ []string) error { queued = text; return nil }
	if err := DeliverCodexChat(context.Background(), t.TempDir(), t.TempDir(), "codex-long", ChatInbound{ID: "message-long", Text: want}); err != nil {
		t.Fatal(err)
	}
	if queued != want {
		t.Fatalf("queued prompt changed: got %d bytes, want %d", len(queued), len(want))
	}
}

func TestDeliverCodexChatLeavesUnsentTUIDraftUntouched(t *testing.T) {
	stubCodexDeliveryHealth(t)
	originalQueue, originalTmux := CodexQueueMessage, tmuxCommand
	t.Cleanup(func() { CodexQueueMessage, tmuxCommand = originalQueue, originalTmux })
	queued := false
	CodexQueueMessage = func(context.Context, string, string, string, string, string, []string) error {
		queued = true
		return nil
	}
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		t.Fatalf("queue delivery touched the TUI: %v", args)
		return nil, nil
	}
	err := DeliverCodexChat(context.Background(), t.TempDir(), t.TempDir(), "codex-draft", ChatInbound{ID: "message-draft", Text: "new chat message"})
	if err != nil || !queued {
		t.Fatalf("draft delivery err=%v queued=%t", err, queued)
	}
}

func TestNewestCodexThreadDoesNotPinRememberedConversation(t *testing.T) {
	threads := []any{
		map[string]any{"id": "new-visible", "recencyAt": float64(200)},
		map[string]any{"id": "old-remembered", "recencyAt": float64(100)},
	}
	if got := newestCodexThread(threads, "old-remembered"); got != "new-visible" {
		t.Fatalf("post-/new delivery selected %q, want the newly active thread", got)
	}
	withoutRecency := []any{
		map[string]any{"id": "first"},
		map[string]any{"id": "old-remembered"},
	}
	if got := newestCodexThread(withoutRecency, "old-remembered"); got != "old-remembered" {
		t.Fatalf("old server fallback selected %q, want remembered thread", got)
	}
}
func TestStartCodexChatPassesInitialMessageAndImagesAsArguments(t *testing.T) {
	stubRegisteredAgent(t, "codex")
	stubCodexBackend(t)
	home := t.TempDir()
	var encoded bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.RGBA{R: 80, G: 20, B: 180, A: 255})
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	original := tmuxCommand
	t.Cleanup(func() { tmuxCommand = original })
	var calls [][]string
	tmuxCommand = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		if stdin != "" || len(args) > 0 && (args[0] == "load-buffer" || args[0] == "paste-buffer" || args[0] == "capture-pane") {
			t.Fatal("Codex startup used terminal text delivery")
		}
		calls = append(calls, append([]string(nil), args...))
		if len(args) > 0 && args[0] == "has-session" {
			return nil, errors.New("missing")
		}
		return nil, nil
	}
	inbound := ChatInbound{ID: "message-1", Text: "Inspect this purple image", Images: []ChatEventImage{{Name: "purple.png", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(encoded.Bytes())}}}
	if err := StartCodexChat(context.Background(), t.TempDir(), home, "codex-chat", inbound); err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, call := range calls {
		joined += strings.Join(call, "\n") + "\n"
	}
	if !strings.Contains(joined, "new-session\n-d\n-s\ncodex-chat") || !strings.Contains(joined, "\n-i\n") || !strings.Contains(joined, "Inspect this purple image") || !strings.Contains(joined, "image-1.png") {
		t.Fatalf("initial Codex arguments were incomplete: %q", joined)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", "codex-chat", "message-1.json")); !os.IsNotExist(err) {
		t.Fatalf("initial inbox envelope was retained: %v", err)
	}
}

func TestStartCodexChatChunksLongInitialMessageAfterStartup(t *testing.T) {
	stubRegisteredAgent(t, "codex")
	stubCodexBackend(t)
	home, root := t.TempDir(), t.TempDir()
	originalCommand, originalSettle, originalSubmit := tmuxCommand, agentReadySettlePause, tmuxSubmitPause
	t.Cleanup(func() {
		tmuxCommand, agentReadySettlePause, tmuxSubmitPause = originalCommand, originalSettle, originalSubmit
	})
	agentReadySettlePause = func(context.Context) error { return nil }
	tmuxSubmitPause = func(context.Context) error { return nil }
	message := strings.Repeat("long🙂", 12_000)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	var chunks []string
	newSessionIncludedPrompt := false
	newSessionIncludedImage := false
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 0 {
			return nil, nil
		}
		switch args[0] {
		case "has-session":
			return nil, errors.New("missing")
		case "capture-pane":
			return []byte("OpenAI Codex (v0.155.1)\n› Ask Codex to do anything"), nil
		case "new-session":
			for _, arg := range args {
				if arg == message {
					newSessionIncludedPrompt = true
				}
				if strings.Contains(arg, "image-1.png") {
					newSessionIncludedImage = true
				}
			}
		case "send-keys":
			if len(args) >= 5 && args[3] == "-l" {
				chunks = append(chunks, args[4])
			}
		}
		return nil, nil
	}
	inbound := ChatInbound{ID: "message-long-start", Text: message, Images: []ChatEventImage{{MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(encoded.Bytes())}}}
	if err := StartCodexChat(context.Background(), root, home, "codex-long-start", inbound); err != nil {
		t.Fatal(err)
	}
	if newSessionIncludedPrompt {
		t.Fatal("long initial prompt was passed through tmux new-session")
	}
	if !newSessionIncludedImage {
		t.Fatal("long initial prompt did not attach its image to Codex startup")
	}
	if got := strings.Join(chunks, ""); got != message || len(chunks) < 2 {
		t.Fatalf("chunked initial prompt has %d bytes in %d chunks, want %d bytes", len(got), len(chunks), len(message))
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", "codex-long-start", "message-long-start.json")); !os.IsNotExist(err) {
		t.Fatalf("delivered initial envelope remained: %v", err)
	}
}

func TestNameCodexChatThreadReturnsAfterItWasNamed(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "chat", "codex-named")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "codex-chat"), []byte("named\n"), 0600); err != nil {
		t.Fatal(err)
	}
	original := tmuxCommand
	t.Cleanup(func() { tmuxCommand = original })
	tmuxCommand = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("already named Codex thread inspected its terminal")
		return nil, nil
	}
	if err := NameCodexChatThread(context.Background(), root, "codex-chat"); err != nil {
		t.Fatal(err)
	}
}

func TestNameCodexChatThreadWaitsThenRecordsCompletion(t *testing.T) {
	root := t.TempDir()
	originalCommand, originalNaming, originalSettle, originalSubmit, originalConfirm := tmuxCommand, codexThreadNamingPause, agentReadySettlePause, tmuxSubmitPause, tmuxSubmitConfirmPause
	t.Cleanup(func() {
		tmuxCommand, codexThreadNamingPause, agentReadySettlePause, tmuxSubmitPause, tmuxSubmitConfirmPause = originalCommand, originalNaming, originalSettle, originalSubmit, originalConfirm
	})
	var calls []string
	namingWaited := false
	codexThreadNamingPause = func(context.Context) error { namingWaited = true; return nil }
	agentReadySettlePause = func(context.Context) error { return nil }
	tmuxSubmitPause = func(context.Context) error { return nil }
	tmuxSubmitConfirmPause = func(context.Context) error { return nil }
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if len(args) > 0 && args[0] == "capture-pane" {
			return []byte("OpenAI Codex\n› Ask Codex to do anything"), nil
		}
		return nil, nil
	}
	if err := NameCodexChatThread(context.Background(), root, "codex-chat"); err != nil {
		t.Fatal(err)
	}
	if !namingWaited || !strings.Contains(strings.Join(calls, "\n"), "load-buffer") {
		t.Fatalf("naming sequence was incomplete: waited=%t calls=%v", namingWaited, calls)
	}
	if data, err := os.ReadFile(filepath.Join(root, "chat", "codex-named", "codex-chat")); err != nil || string(data) != "named\n" {
		t.Fatalf("naming completion marker: %q, %v", data, err)
	}
}

func TestChatMessageToolPersistsStandaloneMessageWithShortEventID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "claude-chat")
	result, err := callDesktopTool(context.Background(), "assignment", "chat_message", json.RawMessage(`{"text":"Working on it"}`))
	if err != nil || result["isError"] == true {
		t.Fatalf("chat_message failed: %v %+v", err, result)
	}
	event, found, err := PullChatEvent(home, "claude-chat")
	if err != nil || !found {
		t.Fatalf("event unavailable: %v", err)
	}
	if event.Kind != "reply" || event.ReplyTo != "" || event.Text != "Working on it" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if len(event.ID) > 16 || strings.Trim(event.ID, "0123456789abcdef") != "" {
		t.Fatalf("event id should be a short token, got %q", event.ID)
	}
}

func TestChatMessageToolAnswersAReference(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "claude-chat")
	if _, err := callDesktopTool(context.Background(), "assignment", "chat_message", json.RawMessage(`{"replyTo":"abc123","text":"here"}`)); err != nil {
		t.Fatal(err)
	}
	event, found, err := PullChatEvent(home, "claude-chat")
	if err != nil || !found || event.ReplyTo != "abc123" || event.Text != "here" {
		t.Fatalf("unexpected event: found=%t err=%v event=%+v", found, err, event)
	}
}

func TestRemovedChatReplyToolIsRejected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "claude-chat")
	if _, err := callDesktopTool(context.Background(), "assignment", "chat_reply", json.RawMessage(`{"replyTo":"abc123","text":"legacy call"}`)); err == nil {
		t.Fatal("chat_reply was removed and must not be accepted")
	}
}

func TestChatAskAllowsStandaloneQuestion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "claude-chat")
	if _, err := callDesktopTool(context.Background(), "assignment", "chat_ask", json.RawMessage(`{"question":"Pick colors","choices":["purple"]}`)); err != nil {
		t.Fatal(err)
	}
	event, found, err := PullChatEvent(home, "claude-chat")
	if err != nil || !found || event.Kind != "question" || event.ReplyTo != "" || event.Question == nil {
		t.Fatalf("unexpected question: found=%t err=%v event=%+v", found, err, event)
	}
}

// stubCodexBackend keeps tests off a real app server while still exercising the
// path that starts one.
func stubCodexBackend(t *testing.T) {
	t.Helper()
	original := EnsureCodexAppServer
	t.Cleanup(func() { EnsureCodexAppServer = original })
	EnsureCodexAppServer = func(context.Context, string) error { return nil }
}

func stubCodexDeliveryHealth(t *testing.T) {
	t.Helper()
	stubCodexBackend(t)
	original, originalMCP := RecoverInterruptedCodexSession, RecoverCodexMCPStartup
	t.Cleanup(func() { RecoverInterruptedCodexSession, RecoverCodexMCPStartup = original, originalMCP })
	RecoverInterruptedCodexSession = func(context.Context, string, string) error { return nil }
	RecoverCodexMCPStartup = func(context.Context, string) error { return nil }
}

// Codex's MCP server is a child of the app server, so the tmux session holding
// it is the internal one. Its chat replies still belong to the conversation the
// controller reads, which is the same name without the prefix.
func TestChatSessionUnwrapsTheCodexAppServerSession(t *testing.T) {
	t.Setenv("VMBOX_CHAT_SESSION", codexAppServerSession("codex-abc123"))
	session, err := chatSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if session != "codex-abc123" {
		t.Fatalf("chat replies would be filed under %q", session)
	}
	t.Setenv("VMBOX_CHAT_SESSION", "codex-abc123")
	if session, err = chatSession(context.Background()); err != nil || session != "codex-abc123" {
		t.Fatalf("an ordinary session must pass through: %q %v", session, err)
	}
}
