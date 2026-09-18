package controller

import (
	"strings"
	"testing"
)

func TestChatInstructionDefaultIsCompact(t *testing.T) {
	got := (&Server{}).chatInstruction("m1", "claude", 1)
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

// TestChatInstructionRepeatsEveryThirdMessage keeps the full envelope on the
// first message and then periodically; every other message carries the short
// [sent via chat] reminder that still routes replies through chat_message.
func TestChatInstructionRepeatsEveryThirdMessage(t *testing.T) {
	server := &Server{}
	want := map[int]bool{1: true, 2: false, 3: false, 4: true, 5: false, 6: false, 7: true, 10: true}
	for ordinal, envelope := range want {
		got := server.chatInstruction("m1", "claude", ordinal)
		hasEnvelope := strings.Contains(got, "vmbox Agent chat message m1]")
		if hasEnvelope != envelope {
			t.Fatalf("ordinal %d: envelope=%q, want envelope=%t", ordinal, got, envelope)
		}
		if !envelope {
			for _, fragment := range []string{"[sent via vmbox Agent chat m1", "chat_message MCP tool (replyTo m1)", "not the terminal"} {
				if !strings.Contains(got, fragment) {
					t.Fatalf("ordinal %d reminder must mention %q: %q", ordinal, fragment, got)
				}
			}
			if len(got) > 170 {
				t.Fatalf("ordinal %d reminder must stay compact, got %d characters: %q", ordinal, len(got), got)
			}
		}
		if got == "" {
			t.Fatalf("ordinal %d must always carry either the envelope or the reply reminder", ordinal)
		}
	}
}

func TestChatInstructionEveryMessageOverride(t *testing.T) {
	server := &Server{ChatInstructionEvery: 1}
	for _, ordinal := range []int{1, 2, 3, 7} {
		if got := server.chatInstruction("m1", "claude", ordinal); got == "" {
			t.Fatalf("ordinal %d must carry the envelope when every message is requested", ordinal)
		}
	}
}

func TestChatInstructionTemplateOverride(t *testing.T) {
	s := &Server{ChatInstructionTemplate: "\nchat %s"}
	got := s.chatInstruction("abc", "claude", 1)
	if !strings.Contains(got, "chat abc") || strings.Contains(got, "chat_message") {
		t.Fatalf("override must replace the default envelope: %q", got)
	}
}

func TestChatInstructionDisabled(t *testing.T) {
	s := &Server{ChatInstructionTemplate: "off"}
	if got := s.chatInstruction("abc", "claude", 1); got != "" {
		t.Fatalf("off must disable the envelope, got %q", got)
	}
}

func TestChatInstructionSkipsShell(t *testing.T) {
	if got := (&Server{}).chatInstruction("abc", "shell", 1); got != "" {
		t.Fatalf("shell agents never get the chat envelope, got %q", got)
	}
}

// The envelope used to be formatted with a fixed number of arguments, so the
// default template reached the agent with Go's %!(EXTRA string=...) appended.
func TestChatInstructionNeverLeaksFormatComplaints(t *testing.T) {
	servers := map[string]*Server{
		"default":     {},
		"one":         {ChatInstructionTemplate: "\nchat %s"},
		"three":       {ChatInstructionTemplate: "\n%s %s %s"},
		"none":        {ChatInstructionTemplate: "\nreply in chat"},
		"every-third": {ChatInstructionEvery: 3},
	}
	for name, server := range servers {
		for _, ordinal := range []int{1, 2, 3, 4} {
			got := server.chatInstruction("m1", "claude", ordinal)
			if strings.Contains(got, "%!") || strings.Contains(got, "%s") {
				t.Fatalf("%s ordinal %d leaked formatting: %q", name, ordinal, got)
			}
		}
	}
	if got := (&Server{ChatInstructionTemplate: "\n%s %s %s"}).chatInstruction("m1", "claude", 1); !strings.Contains(got, "m1 m1 m1") {
		t.Fatalf("every placeholder must be filled: %q", got)
	}
}
