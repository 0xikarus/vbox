package activityphrase

import (
	"regexp"
	"strings"
)

var ongoingAgentAction = regexp.MustCompile(`(?i)\b(?:i['’]m|i am|we['’]re|we are)\s+([a-z]{3,}ing)\b([^.!?;:]*)`)
var nowAgentAction = regexp.MustCompile(`(?i)(?:^|[.!?]\s+)now\s+([a-z]{3,}ing)\b([^.!?;:]*)`)
var intendedAgentAction = regexp.MustCompile(`(?i)(?:^|[.!?]\s+)(?:i['’]ll|i will|we['’]ll|we will|let['’]s)\s+(?:now\s+)?([a-z]{3,})\b([^.!?;:]*)`)
var activityActionTail = regexp.MustCompile(`(?i)\s+(?:and|but|while|so|then)\b`)

var activityFillerAfterVerb = map[string]bool{
	"a": true, "an": true, "the": true, "it": true, "its": true,
	"them": true, "that": true, "this": true, "my": true, "our": true,
}
var activityTailPreposition = map[string]bool{
	"a": true, "an": true, "the": true, "for": true, "to": true,
	"with": true, "on": true, "in": true, "at": true, "of": true,
	"from": true, "by": true, "onto": true,
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
		words := activityWords.FindAllString(rest, -1)
		for len(words) > 0 && activityFillerAfterVerb[strings.ToLower(words[0])] {
			words = words[1:]
		}
		if len(words) > 3 {
			words = words[:3]
		}
		for len(words) > 0 && activityTailPreposition[strings.ToLower(words[len(words)-1])] {
			words = words[:len(words)-1]
		}
		if len(words) == 0 {
			return ""
		}
		phrase := normalizeActivity(verb + " " + strings.Join(words, " "))
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
	words := activityWords.FindAllString(rest, -1)
	for len(words) > 0 && activityFillerAfterVerb[strings.ToLower(words[0])] {
		words = words[1:]
	}
	if len(words) > 2 {
		words = words[:2]
	}
	for len(words) > 0 && activityTailPreposition[strings.ToLower(words[len(words)-1])] {
		words = words[:len(words)-1]
	}
	if len(words) == 0 {
		return ""
	}
	return normalizeActivity("Preparing to " + verb + " " + strings.Join(words, " "))
}

func lastActionMatch(pattern *regexp.Regexp, line string) []string {
	matches := pattern.FindAllStringSubmatch(line, -1)
	if len(matches) == 0 {
		return nil
	}
	return matches[len(matches)-1]
}
