package activityphrase

import (
	"regexp"
	"strings"
)

var ongoingAgentAction = regexp.MustCompile(`(?i)\b(?:i['’]m|i am|we['’]re|we are)\s+([a-z]{3,}ing)\b([^.!?;:]*)`)
var nowAgentAction = regexp.MustCompile(`(?i)(?:^|[.!?]\s+)now\s+([a-z]{3,}ing)\b([^.!?;:]*)`)
var intendedAgentAction = regexp.MustCompile(`(?i)(?:^|[.!?]\s+)(?:i['’]ll|i will|we['’]ll|we will|let['’]s)\s+(?:now\s+)?([a-z]{3,})\b([^.!?;:]*)`)
var activityActionTail = regexp.MustCompile(`(?i)\s+(?:and|but|while|so|then)\b`)
var activityProseWords = regexp.MustCompile(`[\pL][\pL\pN'’-]*|[0-9]+`)

var activityFillerAfterVerb = map[string]bool{
	"a": true, "an": true, "the": true, "it": true, "its": true,
	"them": true, "that": true, "this": true, "my": true, "our": true,
}
var activityIncompleteEnding = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "if": true,
	"there's": true, "there’s": true, "they": true, "are": true,
	"for": true, "your": true, "next": true, "with": true, "on": true,
	"step": true, "as": true, "until": true, "before": true, "after": true,
	"of": true, "in": true, "at": true, "from": true, "by": true,
	"onto": true, "into": true, "through": true, "over": true,
	"under": true, "about": true, "around": true,
	"my": true, "our": true, "their": true, "its": true, "this": true,
	"that": true, "these": true, "those": true, "other": true, "top": true,
	"first": true, "last": true,
}
var activityNonObjects = map[string]bool{
	"it": true, "them": true, "something": true, "someone": true,
	"anything": true, "anyone": true, "thing": true, "one": true,
}

// completeProsePhrase considers up to six source words, then picks the longest
// complete phrase that fits the display limits. A dangling connector may be
// completed by the next word; otherwise the phrase ends before that connector.
func completeProsePhrase(prefix string, words []string) string {
	if len(words) > 6 {
		words = words[:6]
	}
	prefixWords := len(strings.Fields(prefix))
	var complete string
	for end := 1; end <= len(words) && prefixWords+end <= 5; end++ {
		object := words[:end]
		last := strings.ToLower(object[len(object)-1])
		if activityIncompleteEnding[last] || danglingProseClause(object) {
			continue
		}
		if strings.EqualFold(prefix, "waiting") && !realWaitingObject(object) {
			continue
		}
		phrase := normalizeActivity(prefix + " " + strings.Join(object, " "))
		if len(strings.Fields(phrase)) == prefixWords+end {
			complete = phrase
		}
	}
	return complete
}

func danglingProseClause(words []string) bool {
	for i, word := range words {
		if !strings.EqualFold(word, "until") {
			continue
		}
		if len(words)-i < 2 {
			return true
		}
		next := strings.ToLower(words[i+1])
		if (next == "the" || next == "a" || next == "an" || next == "your" || next == "my") && len(words)-i < 4 {
			return true
		}
	}
	return len(words) >= 2 && strings.EqualFold(words[len(words)-2], "a") && strings.EqualFold(words[len(words)-1], "speed")
}

func realWaitingObject(words []string) bool {
	if len(words) < 2 || (strings.ToLower(words[0]) != "for" && strings.ToLower(words[0]) != "on") {
		return false
	}
	for _, word := range words[1:] {
		lower := strings.ToLower(word)
		if !activityIncompleteEnding[lower] && !activityNonObjects[lower] {
			return true
		}
	}
	return false
}

// CompleteEnding rejects model phrases that stop on a connector or an
// unresolved waiting object.
func CompleteEnding(phrase string) bool {
	words := strings.Fields(phrase)
	if len(words) == 0 || activityIncompleteEnding[strings.ToLower(words[len(words)-1])] {
		return false
	}
	if !strings.EqualFold(words[0], "waiting") {
		return true
	}
	return len(words) > 1 && realWaitingObject(words[1:])
}

var completedWork = regexp.MustCompile(`(?i)\b(?:done|finished|complete|completed)\b`)
var waitingForNewWork = regexp.MustCompile(`(?i)\b(?:waiting|wait)\b[^.!?\n]{0,80}\b(?:instructions?|next\s+(?:task|message|request|assignment)|your\s+(?:next\s+)?(?:task|message|request|assignment))\b`)

// CompletedAndWaitingForWork recognizes a finished job followed by a request
// for more work. The explicit idle state takes precedence over model guesses
// about a script or approval.
func CompletedAndWaitingForWork(text string) bool {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 0 {
		return false
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if strings.HasPrefix(last, "tool: ") || !waitingForNewWork.MatchString(last) {
		return false
	}
	tail := last
	if len(lines) > 1 {
		previous := strings.TrimSpace(lines[len(lines)-2])
		if !strings.HasPrefix(previous, "tool: ") && !strings.HasPrefix(previous, "user: ") {
			tail = previous + " " + last
		}
	}
	return completedWork.MatchString(tail)
}

// ExplicitProseActivity takes a current, first-person action from the newest
// agent prose line. It is a conservative fallback when the generator score is
// low: a reported worker action or an older plan cannot become the subtitle.
func ExplicitProseActivity(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "tool: ") || strings.HasPrefix(line, "user: ") {
			continue
		}
		line = strings.TrimPrefix(line, "assistant: ")
		match := lastActionMatch(ongoingAgentAction, line)
		if match == nil {
			match = lastActionMatch(nowAgentAction, line)
		}
		if match == nil {
			return ""
		}
		verb, rest := match[1], match[2]
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rest)), "that ") {
			return ""
		}
		if index := activityActionTail.FindStringIndex(rest); index != nil {
			rest = rest[:index[0]]
		}
		words := activityProseWords.FindAllString(rest, -1)
		for len(words) > 0 && activityFillerAfterVerb[strings.ToLower(words[0])] {
			words = words[1:]
		}
		phrase := completeProsePhrase(verb, words)
		if len(strings.Fields(phrase)) < 2 {
			return ""
		}
		return phrase
	}
	return ""
}

// ExplicitIntentActivity describes a next action stated by the agent itself.
// "Preparing to" avoids reporting a plan as already completed or underway.
// The verb and object come from the current transcript, not a fixed label list.
func ExplicitIntentActivity(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 0 {
		return ""
	}
	line := strings.TrimSpace(lines[len(lines)-1])
	if line == "" || strings.HasPrefix(line, "tool: ") || strings.HasPrefix(line, "user: ") {
		return ""
	}
	line = strings.TrimPrefix(line, "assistant: ")
	match := lastActionMatch(intendedAgentAction, line)
	if match == nil {
		return ""
	}
	verb, rest := strings.ToLower(match[1]), match[2]
	if verb == "have" || verb == "be" || verb == "get" || verb == "let" || verb == "say" {
		return ""
	}
	if index := activityActionTail.FindStringIndex(rest); index != nil {
		rest = rest[:index[0]]
	}
	words := activityProseWords.FindAllString(rest, -1)
	for len(words) > 0 && activityFillerAfterVerb[strings.ToLower(words[0])] {
		words = words[1:]
	}
	return completeProsePhrase("Preparing to "+verb, words)
}

func lastActionMatch(pattern *regexp.Regexp, line string) []string {
	matches := pattern.FindAllStringSubmatch(line, -1)
	if len(matches) == 0 {
		return nil
	}
	return matches[len(matches)-1]
}
