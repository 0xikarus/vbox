package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

var (
	terminalANSI       = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	terminalChoice     = regexp.MustCompile(`^\s*([›>])?\s*([1-9][0-9]*)[.)]\s+(.+?)\s*$`)
	terminalMenuChoice = regexp.MustCompile(`^(?:\s*(❯)\s+|\s{2,})(\S.*?)\s*$`)
)

// detectTerminalPrompt deliberately recognizes only a numbered or cursor menu
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

	firstChoice, choices := numberedTerminalChoices(lines, last)
	menu := false
	if len(choices) < 2 {
		firstChoice, choices = menuTerminalChoices(lines, last)
		menu = len(choices) >= 2
	}
	if len(choices) < 2 || len(choices) > 9 {
		return nil
	}

	title := terminalPromptTitle(lines, firstChoice, menu)
	fingerprint := title + "\n" + strings.TrimSpace(lines[last])
	for _, choice := range choices {
		fingerprint += "\n" + choice.Value + "\x00" + choice.Label
	}
	digest := sha256.Sum256([]byte(fingerprint))
	return &v1.TerminalPrompt{ID: hex.EncodeToString(digest[:12]), Text: title, Choices: choices}
}

func numberedTerminalChoices(lines []string, last int) (int, []v1.TerminalPromptChoice) {
	firstChoice, selected := -1, -1
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
		if match[1] != "" {
			selected = len(choices)
		}
		choices = append(choices, v1.TerminalPromptChoice{Value: match[2], Label: strings.TrimSpace(match[3]), Input: match[2], Submit: true})
	}
	for left, right := 0, len(choices)-1; left < right; left, right = left+1, right-1 {
		choices[left], choices[right] = choices[right], choices[left]
	}
	if selected >= 0 {
		selected = len(choices) - 1 - selected
		for index := range choices {
			delta := index - selected
			if delta < 0 {
				choices[index].Input = strings.Repeat("\x1b[A", -delta) + "\r"
			} else {
				choices[index].Input = strings.Repeat("\x1b[B", delta) + "\r"
			}
			choices[index].Submit = false
		}
	}
	return firstChoice, choices
}

func menuTerminalChoices(lines []string, last int) (int, []v1.TerminalPromptChoice) {
	firstChoice, selected := -1, -1
	choices := make([]v1.TerminalPromptChoice, 0, 4)
	for index := last - 1; index >= 0 && last-index <= 20; index-- {
		if strings.TrimSpace(lines[index]) == "" {
			continue
		}
		match := terminalMenuChoice.FindStringSubmatch(lines[index])
		if match == nil {
			if len(choices) > 0 {
				break
			}
			continue
		}
		firstChoice = index
		if match[1] == "❯" {
			selected = len(choices)
		}
		choices = append(choices, v1.TerminalPromptChoice{Label: strings.TrimSpace(match[2])})
	}
	if selected < 0 {
		return -1, nil
	}
	for left, right := 0, len(choices)-1; left < right; left, right = left+1, right-1 {
		choices[left], choices[right] = choices[right], choices[left]
	}
	selected = len(choices) - 1 - selected
	for index := range choices {
		choices[index].Value = strconv.Itoa(index + 1)
		delta := index - selected
		if delta < 0 {
			choices[index].Input = strings.Repeat("\x1b[A", -delta) + "\r"
		} else {
			choices[index].Input = strings.Repeat("\x1b[B", delta) + "\r"
		}
	}
	return firstChoice, choices
}

func terminalPromptTitle(lines []string, firstChoice int, menu bool) string {
	title := "Terminal input required"
	for index := firstChoice - 1; index >= 0 && firstChoice-index <= 8; index-- {
		candidate := strings.TrimSpace(lines[index])
		lower := strings.ToLower(candidate)
		if candidate == "" || lower == "security guide" || strings.HasPrefix(lower, "release notes:") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			continue
		}
		if menu && !strings.Contains(candidate, "?") {
			continue
		}
		title = strings.TrimSpace(strings.TrimLeft(candidate, "✨›>•* "))
		break
	}
	return title
}

func terminalPromptCue(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	for _, cue := range []string{"press enter", "enter to continue", "enter to confirm", "select an option", "choose an option", "choose one", "use arrow", "choice ["} {
		if strings.Contains(lower, cue) {
			return true
		}
	}
	return false
}
