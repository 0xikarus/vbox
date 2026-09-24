package controller

import (
	"strings"
	"testing"
)

func TestChatInstructionDefaultRemindsAgentToReplyThroughMCP(t *testing.T) {
	got := (&Server{}).chatInstruction("m1", "claude", 1)
	for _, agent := range []string{"codex", "opencode"} {
		if other := (&Server{}).chatInstruction("m1", agent, 1); other != got {
			t.Fatalf("%s must receive the same envelope as claude: %q", agent, other)
		}
	}
	if got != "\n\n[Message-ID: m1]\nReply using the vmbox-desktop chat_message MCP tool with replyTo set to this Message-ID." {
		t.Fatalf("default appendix should carry the reply ID and MCP reminder: %q", got)
	}
}

func TestChatInstructionIsThinOnEveryMessage(t *testing.T) {
	server := &Server{}
	for _, ordinal := range []int{1, 2, 3, 4, 5, 6, 7, 10} {
		got := server.chatInstruction("m1", "claude", ordinal)
		if got != "\n\n[Message-ID: m1]\nReply using the vmbox-desktop chat_message MCP tool with replyTo set to this Message-ID." {
			t.Fatalf("ordinal %d appendix: %q", ordinal, got)
		}
	}
}

func TestContactChatInstructionKeepsRoutingAndRemindsAgentToReplyThroughMCP(t *testing.T) {
	got := (&Server{}).contactChatInstruction("m1", "box-2", "Helper", "claude")
	if got != "\n\n[Message-ID: m1; From-Box-ID: box-2]\nReply using the vmbox-desktop chat_message MCP tool with contact set to this From-Box-ID." {
		t.Fatalf("contact appendix must carry IDs and MCP reminder: %q", got)
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
