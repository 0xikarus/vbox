package boxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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
