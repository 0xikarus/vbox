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

func TestChatReplyToolPersistsTextAndImageForController(t *testing.T) {
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
	result, err := callDesktopTool(context.Background(), "assignment", "chat_reply", args)
	if err != nil || result["isError"] == true {
		t.Fatalf("chat_reply failed: %v %+v", err, result)
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
	inbound := ChatInbound{ID: "message-1", Text: "Inspect [Image 1]", Images: []ChatEventImage{{Name: "purple.png", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(encoded.Bytes())}}}
	if err := DeliverCodexChat(context.Background(), home, "codex-chat", inbound); err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || !reflect.DeepEqual(got[:4], []string{"queue", "--thread", "codex-chat", "--message"}) {
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
