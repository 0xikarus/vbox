package controller

import (
	"strings"
	"testing"
)

func TestChatInstructionDefaultIsCompact(t *testing.T) {
	got := (&Server{}).chatInstruction("m1", "claude")
	if !strings.Contains(got, "vmbox Agent chat message m1") {
		t.Fatalf("default envelope must carry the message id: %q", got)
	}
	for _, fragment := range []string{"chat_message tool with replyTo m1", "chat_ask with the same replyTo", "PNG/JPEG/GIF", "computer tools (desktop_screenshot, desktop_click, desktop_type, desktop_key)"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("default envelope must mention %q: %q", fragment, got)
		}
	}
	if strings.Contains(got, "complete response text") || strings.Contains(got, "multiple flag") {
		t.Fatalf("old verbose envelope still present")
	}
	if len(defaultChatInstruction) > 480 {
		t.Fatalf("default envelope should stay short, got %d characters", len(defaultChatInstruction))
	}
	if strings.Count(got, "\n") > 3 {
		t.Fatalf("envelope should stay compact (max 3 newlines), got %d", strings.Count(got, "\n"))
	}
}

func TestChatInstructionTemplateOverride(t *testing.T) {
	s := &Server{ChatInstructionTemplate: "\nchat %s"}
	got := s.chatInstruction("abc", "claude")
	if !strings.Contains(got, "chat abc") || strings.Contains(got, "chat_message tool") {
		t.Fatalf("override must replace the default envelope: %q", got)
	}
}

func TestChatInstructionDisabled(t *testing.T) {
	s := &Server{ChatInstructionTemplate: "off"}
	if got := s.chatInstruction("abc", "claude"); got != "" {
		t.Fatalf("off must disable the envelope, got %q", got)
	}
}

func TestChatInstructionSkipsShell(t *testing.T) {
	if got := (&Server{}).chatInstruction("abc", "shell"); got != "" {
		t.Fatalf("shell agents never get the chat envelope, got %q", got)
	}
}
