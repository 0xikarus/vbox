package controller

import "testing"

func TestDetectTerminalPromptRecognizesCodexUpdateChoice(t *testing.T) {
	content := "Research the chain\n\n  ✨ Update available! 0.153.0 -> 0.153.2\n\n  Release notes: https://example.test/releases\n\n› 1. Update now (runs `npm install`)\n  2. Skip\n  3. Skip until next version\n\n  Press enter to continue\n\n"
	prompt := detectTerminalPrompt(content)
	if prompt == nil {
		t.Fatal("Codex update prompt was not detected")
	}
	if prompt.Text != "Update available! 0.153.0 -> 0.153.2" || len(prompt.Choices) != 3 {
		t.Fatalf("prompt=%+v", prompt)
	}
	if prompt.Choices[1].Value != "2" || prompt.Choices[1].Label != "Skip" || prompt.ID == "" {
		t.Fatalf("choices=%+v id=%q", prompt.Choices, prompt.ID)
	}
	if again := detectTerminalPrompt(content); again == nil || again.ID != prompt.ID {
		t.Fatalf("prompt ID is not stable: first=%+v second=%+v", prompt, again)
	}
}

func TestDetectTerminalPromptRejectsNumberedOutputWithoutBottomCue(t *testing.T) {
	for _, content := range []string{
		"Results\n1. alpha\n2. beta\nfinished\n",
		"Choose an option\n1. alpha\n2. beta\ncontinuing work now\n",
		"1. only one choice\nPress enter to continue\n",
	} {
		if prompt := detectTerminalPrompt(content); prompt != nil {
			t.Fatalf("false prompt for %q: %+v", content, prompt)
		}
	}
}

func TestDetectTerminalPromptStripsANSI(t *testing.T) {
	content := "\x1b[32mChoose mode\x1b[0m\n  1) Safe\n  2) Fast\nChoice [1]:\n"
	prompt := detectTerminalPrompt(content)
	if prompt == nil || prompt.Text != "Choose mode" || len(prompt.Choices) != 2 {
		t.Fatalf("prompt=%+v", prompt)
	}
}
