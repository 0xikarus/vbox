package activityphrase

import (
	"regexp"
	"strings"
	"unicode"
)

// Candidate is an activity phrase and its position in a transcript excerpt.
type Candidate struct {
	Text    string
	Line    int
	Recency int
	Tool    bool
}

var activityWords = regexp.MustCompile(`[\pL][\pL\pN'’-]*`)

var activityStops = map[string]bool{
	"a": true, "an": true, "and": true, "but": true, "or": true, "then": true,
	"because": true, "while": true, "so": true, "with": true, "without": true,
	"that": true, "which": true, "when": true, "where": true, "if": true,
}

var activityNonVerbs = map[string]bool{
	"a": true, "an": true, "the": true, "i": true, "i'm": true, "i’m": true,
	"we": true, "you": true, "he": true, "she": true, "it": true, "they": true,
	"this": true, "that": true, "these": true, "those": true, "there": true,
	"here": true, "my": true, "your": true, "our": true, "their": true,
	"yes": true, "no": true, "thanks": true, "okay": true, "ok": true,
	"not": true, "only": true, "still": true, "maybe": true, "also": true,
	"just": true, "now": true, "next": true, "first": true, "last": true,
	"new": true, "good": true, "bad": true, "great": true, "done": true,
	"ready": true, "current": true, "recent": true, "latest": true,
}

var activityImperativeVerbs = map[string]bool{
	"add": true, "analyze": true, "apply": true, "ask": true, "browse": true,
	"build": true, "check": true, "clean": true, "collect": true, "commit": true,
	"compare": true, "continue": true, "create": true, "debug": true, "delete": true,
	"describe": true, "download": true, "edit": true, "evaluate": true, "extract": true,
	"fetch": true, "find": true, "fix": true, "generate": true, "get": true,
	"handle": true, "implement": true, "inspect": true, "install": true, "keep": true,
	"label": true, "list": true, "load": true, "look": true, "make": true,
	"merge": true, "normalize": true, "open": true, "parse": true, "patch": true,
	"prepare": true, "protect": true, "push": true, "read": true, "rebase": true,
	"remove": true, "render": true, "repair": true, "replace": true, "report": true,
	"rerun": true, "review": true, "run": true, "save": true, "scan": true,
	"search": true, "send": true, "set": true, "show": true, "start": true,
	"stop": true, "summarize": true, "test": true, "train": true, "try": true,
	"update": true, "upload": true, "use": true, "validate": true, "verify": true,
	"view": true, "wait": true, "write": true,
}

// Candidates finds short verb phrases in the latest twelve agent lines. Tool
// activity lines are candidates on their own, including the old Running tool
// heartbeat used by workers that have not yet updated.
func Candidates(text string) []Candidate {
	var lines []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line != "" && !strings.HasPrefix(line, "user: ") {
			lines = append(lines, line)
		}
	}
	if len(lines) > 12 {
		lines = lines[len(lines)-12:]
	}
	var result []Candidate
	for lineIndex, line := range lines {
		line = strings.TrimPrefix(line, "assistant: ")
		if tool, ok := strings.CutPrefix(line, "tool: "); ok {
			if phrase := normalizeActivity(tool); phrase != "" {
				result = append(result, Candidate{Text: phrase, Line: lineIndex, Recency: len(lines) - 1 - lineIndex, Tool: true})
			}
			continue
		}
		if line == "Running tool" {
			result = append(result, Candidate{Text: line, Line: lineIndex, Recency: len(lines) - 1 - lineIndex, Tool: true})
			continue
		}
		for _, sentence := range strings.FieldsFunc(line, func(r rune) bool {
			return strings.ContainsRune(".!?;:\n", r)
		}) {
			for _, segment := range strings.Split(sentence, ",") {
				words := activityWords.FindAllString(segment, -1)
				for i, word := range words {
					lower := strings.ToLower(word)
					imperative := i == 0 && activityImperativeVerbs[lower]
					if i == 1 && (strings.EqualFold(words[0], "Now") || strings.EqualFold(words[0], "Please")) {
						imperative = true
					}
					gerund := len([]rune(lower)) >= 5 && strings.HasSuffix(lower, "ing")
					if !imperative && !gerund || activityStops[lower] {
						continue
					}
					phrase := []string{word}
					for j := i + 1; j < len(words) && len(phrase) < 5; j++ {
						if activityStops[strings.ToLower(words[j])] {
							break
						}
						phrase = append(phrase, words[j])
					}
					if len(phrase) < 2 {
						continue
					}
					if strings.EqualFold(phrase[0], "Starting") && strings.EqualFold(phrase[1], "point") {
						continue
					}
					if normalized := normalizeActivity(strings.Join(phrase, " ")); normalized != "" {
						result = append(result, Candidate{Text: normalized, Line: lineIndex, Recency: len(lines) - 1 - lineIndex})
					}
				}
			}
		}
	}
	unique := make([]Candidate, 0, len(result))
	positions := make(map[string]int, len(result))
	for _, candidate := range result {
		if index, exists := positions[candidate.Text]; exists {
			unique[index] = candidate // Retain the most recent occurrence for ranking.
		} else {
			positions[candidate.Text] = len(unique)
			unique = append(unique, candidate)
		}
	}
	return unique
}

func normalizeActivity(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(strings.TrimPrefix(value, "I'm "), "I’m ")
	words := strings.Fields(value)
	if len(words) == 0 {
		return ""
	}
	var kept []string
	for _, word := range words {
		if len([]rune(strings.Join(append(kept, word), " "))) > 32 {
			break
		}
		kept = append(kept, word)
	}
	if len(kept) == 0 {
		return ""
	}
	result := []rune(strings.Join(kept, " "))
	result[0] = unicode.ToUpper(result[0])
	return string(result)
}

// Normalize turns a phrase or teacher span into its short display form.
func Normalize(value string) string { return normalizeActivity(value) }
