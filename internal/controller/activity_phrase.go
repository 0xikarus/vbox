package controller

import (
	_ "embed"
	"regexp"
	"strings"
	"unicode"

	"github.com/0xikarus/vmbox-service/internal/activityphrase"
)

//go:embed activity_model.bin
var activityModelBytes []byte

var activityGenerator = func() *activityphrase.Generator {
	model, err := activityphrase.LoadGenerator(activityModelBytes)
	if err != nil {
		panic(err)
	}
	return model
}()

// The v2 held-out judge found 3 bad model phrases among 94 at this gate.
// The lower prose gate is deliberately limited to recent prose and excludes
// Waiting/Awaiting, which caused all three v2 bad judgments. V3 outputs are
// exported for a fresh semantic judge pass.
const activityConfidenceThreshold = -0.525
const activityProseConfidenceThreshold = -0.65

var activityFileWord = regexp.MustCompile(`(?i)^[^\s/]+\.(?:png|jpe?g|gif|webp|go|js|ts|py|md|json|css|html)$`)

var activityContentStops = map[string]bool{"a": true, "an": true, "the": true, "and": true, "to": true, "for": true, "on": true, "in": true, "with": true, "of": true, "at": true, "from": true, "by": true}
var activityFileVerbs = map[string]bool{
	"editing": true, "reading": true, "reviewing": true, "opening": true,
	"saving": true, "writing": true, "updating": true, "creating": true,
	"checking": true, "merging": true, "renaming": true, "deleting": true,
	"patching": true, "formatting": true, "comparing": true, "inspecting": true,
	"fixing": true, "testing": true, "running": true,
}
var activityTrivialCommands = map[string]bool{"cd": true, "ls": true, "echo": true, "cat": true, "pwd": true, "sleep": true, "true": true, "test": true, "command": true, "tool": true, "gh": true, "shell": true, "bash": true}

// activityPhrase prefers factual harness labels, then generates a short phrase
// from bounded evidence. Low-confidence or invalid generations leave the UI's
// working fallback.
func activityPhrase(text string) string {
	return activityPhraseFromEvidence(text, false)
}

// Observation phrases can also describe an explicit next action in the newest
// agent line. The ordinary generator keeps its calibrated confidence gate.
func observationActivityPhrase(text string) string {
	return activityPhraseFromEvidence(text, true)
}

func activityPhraseFromEvidence(text string, allowIntent bool) string {
	text = activityphrase.NormalizeEvidence(text)
	if text == "" {
		return ""
	}
	if label := activityphrase.LatestToolLabel(text); label != "" {
		return label
	}
	if activityphrase.CompletedAndWaitingForWork(text) {
		return "Idle"
	}
	phrase, confidence := activityGenerator.GenerateScored(text)
	phrase = activityphrase.Normalize(phrase)
	lastLine := text[strings.LastIndexByte(text, '\n')+1:]
	proseTail := !strings.HasPrefix(lastLine, "tool: ")
	if validActivityPhrase(phrase) {
		if confidence >= activityConfidenceThreshold {
			return phrase
		}
		if proseTail && confidence >= activityProseConfidenceThreshold &&
			!strings.HasPrefix(phrase, "Waiting ") && !strings.HasPrefix(phrase, "Awaiting ") &&
			(!strings.HasPrefix(phrase, "Fixing ") || recentOwnFix(text)) {
			return phrase
		}
	}
	if proseTail {
		if fallback := activityphrase.ExplicitProseActivity(text); validActivityPhrase(fallback) {
			return fallback
		}
		if allowIntent {
			if fallback := activityphrase.ExplicitIntentActivity(text); validActivityPhrase(fallback) {
				return fallback
			}
		}
	}
	return ""
}

func recentOwnFix(text string) bool {
	line := text[strings.LastIndexByte(text, '\n')+1:]
	line = strings.TrimSpace(strings.TrimPrefix(line, "assistant: "))
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "fixing ") || strings.HasPrefix(lower, "now fixing ") {
		return true
	}
	return strings.HasPrefix(activityphrase.ExplicitProseActivity(text), "Fixing ")
}

func validActivityPhrase(phrase string) bool {
	words := strings.Fields(phrase)
	if len(words) < 1 || len(words) > 5 || len([]rune(phrase)) > 32 {
		return false
	}
	if !activityphrase.CompleteEnding(phrase) {
		return false
	}
	runes := []rune(phrase)
	if !unicode.IsUpper(runes[0]) {
		return false
	}
	if !strings.HasSuffix(strings.ToLower(words[0]), "ing") {
		return false
	}
	if len(words) > 1 && strings.EqualFold(words[0], "Running") && activityTrivialCommands[strings.ToLower(words[1])] {
		return false
	}
	if strings.EqualFold(phrase, "Using a tool") {
		return false
	}
	if strings.EqualFold(words[0], "Holding") || strings.EqualFold(words[0], "Asking") || strings.EqualFold(words[0], "Waking") {
		for _, word := range words[1:] {
			if strings.EqualFold(word, "screenshots") {
				return false
			}
		}
	}
	if len(words) > 1 && activityFileWord.MatchString(words[1]) && !activityFileVerbs[strings.ToLower(words[0])] {
		return false
	}
	seen := make(map[string]bool, len(words))
	for _, word := range words {
		key := strings.ToLower(word)
		if strings.ContainsAny(word, "<>\n\r") {
			return false
		}
		if activityContentStops[key] {
			continue
		}
		roots := []string{key}
		if strings.HasSuffix(key, "ing") && len(key) > 5 {
			stem := strings.TrimSuffix(key, "ing")
			roots = append(roots, stem, stem+"e")
			if len(stem) > 2 && stem[len(stem)-1] == stem[len(stem)-2] {
				roots = append(roots, stem[:len(stem)-1])
			}
		}
		for _, root := range roots {
			if seen[root] {
				return false
			}
		}
		for _, root := range roots {
			seen[root] = true
		}
	}
	return true
}
