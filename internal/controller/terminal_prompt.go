package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

var (
	terminalANSI   = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	terminalChoice = regexp.MustCompile(`^\s*[›>]?\s*([1-9][0-9]*)[.)]\s+(.+?)\s*$`)
)

// detectTerminalPrompt deliberately recognizes only a numbered choice block
// followed by an explicit input cue at the bottom of the current pane. This
// keeps numbered logs and old scrollback from becoming clickable controls.
func detectTerminalPrompt(content string) *v1.TerminalPrompt {
	clean := terminalANSI.ReplaceAllString(content, "")
	lines := strings.Split(strings.ReplaceAll(clean, "\r", ""), "\n")
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last < 0 || !terminalPromptCue(lines[last]) {
		return nil
	}

	firstChoice := -1
	choices := make([]v1.TerminalPromptChoice, 0, 4)
	for index := last - 1; index >= 0 && last-index <= 20; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		match := terminalChoice.FindStringSubmatch(line)
		if match == nil {
			if len(choices) > 0 {
				break
			}
			continue
		}
		firstChoice = index
		choices = append(choices, v1.TerminalPromptChoice{Value: match[1], Label: strings.TrimSpace(match[2])})
	}
	if len(choices) < 2 || len(choices) > 9 {
		return nil
	}
	for left, right := 0, len(choices)-1; left < right; left, right = left+1, right-1 {
		choices[left], choices[right] = choices[right], choices[left]
	}

	title := "Terminal input required"
	for index := firstChoice - 1; index >= 0 && firstChoice-index <= 8; index-- {
		candidate := strings.TrimSpace(lines[index])
		lower := strings.ToLower(candidate)
		if candidate == "" || strings.HasPrefix(lower, "release notes:") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			continue
		}
		title = strings.TrimSpace(strings.TrimLeft(candidate, "✨›>•* "))
		break
	}

	fingerprint := title + "\n" + strings.TrimSpace(lines[last])
	for _, choice := range choices {
		fingerprint += "\n" + choice.Value + "\x00" + choice.Label
	}
	digest := sha256.Sum256([]byte(fingerprint))
	return &v1.TerminalPrompt{ID: hex.EncodeToString(digest[:12]), Text: title, Choices: choices}
}

func terminalPromptCue(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	for _, cue := range []string{"press enter", "enter to continue", "select an option", "choose an option", "choose one", "use arrow", "choice ["} {
		if strings.Contains(lower, cue) {
			return true
		}
	}
	return false
}
