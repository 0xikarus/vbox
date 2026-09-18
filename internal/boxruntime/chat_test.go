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
	"reflect"
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

func TestDeliverCodexChatReferencesLocalImagesWithoutUnsupportedQueueFlags(t *testing.T) {
	home := t.TempDir()
	var encoded bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.RGBA{R: 80, G: 20, B: 180, A: 255})
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}

	original := runCodexQueue
	t.Cleanup(func() { runCodexQueue = original })
	var got []string
	runCodexQueue = func(_ context.Context, _ string, eventPath string, args []string) error {
		got = append([]string(nil), args...)
		return os.Remove(eventPath)
	}
	// Codex is addressed by the thread id it recorded, not by the tmux session
	// name, so a thread started from a terminal can be reached too.
	root := t.TempDir()
	thread := "01a0b294-9d7d-7243-a1d8-f5caf047d919"
	rollout := filepath.Join(home, ".codex", "sessions", "2026", "09", "18", "rollout-2026-09-18T03-34-39-"+thread+".jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o700); err != nil {
		t.Fatal(err)
	}
	meta := `{"type":"session_meta","payload":{"session_id":"` + thread + `","cwd":"` + WorkspaceDirectory() + `"}}` + "\n"
	if err := os.WriteFile(rollout, []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}

	inbound := ChatInbound{ID: "message-1", Text: "Inspect [Image 1]", Images: []ChatEventImage{{Name: "purple.png", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(encoded.Bytes())}}}
	if err := DeliverCodexChat(context.Background(), root, home, "codex-chat", inbound); err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || !reflect.DeepEqual(got[:4], []string{"queue", "--thread", thread, "--message"}) {
		t.Fatalf("unexpected codex queue arguments: %q", got)
	}
	if strings.Contains(strings.Join(got, "\n"), "-i") || !strings.Contains(got[4], "[Image 1]: ") || !strings.Contains(got[4], "/inbox/codex-chat/files/message-1/image-1.png") {
		t.Fatalf("image was not delivered as a local file reference: %q", got)
	}
}

func TestStartCodexChatPassesInitialMessageAndImagesAsArguments(t *testing.T) {
	stubRegisteredAgent(t, "codex")
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
