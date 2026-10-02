package activityphrase

import (
	"strings"
	"testing"
)

func TestCandidatesFindOpenVocabularyActivities(t *testing.T) {
	text := "assistant: I'm fixing the mobile swipe bug and checking tests.\n" +
		"assistant: Now review the terminal layout, then ship it.\n" +
		"tool: Running go test\n" +
		"assistant: Reading the last log.\n" +
		"Running tool"
	got := Candidates(text)
	wants := []string{"Fixing the mobile swipe bug", "Checking tests", "Review the terminal layout", "Running go test", "Reading the last log", "Running tool"}
	for _, want := range wants {
		found := false
		for _, candidate := range got {
			if candidate.Text == want {
				found = true
				if strings.HasPrefix(want, "Running") && !candidate.Tool {
					t.Errorf("tool candidate lost its marker: %+v", candidate)
				}
			}
		}
		if !found {
			t.Errorf("missing %q in %+v", want, got)
		}
	}
}

func TestCandidatesUseLatestLinesAndWordBoundary(t *testing.T) {
	text := "Fixing an old problem\n" + strings.Repeat("Waiting for the next step\n", 12) + "tool: Editing extraordinarilylongfilename.go"
	got := Candidates(text)
	for _, candidate := range got {
		if strings.Contains(candidate.Text, "old problem") || len([]rune(candidate.Text)) > 32 {
			t.Fatalf("candidate escaped latest lines or length limit: %+v", candidate)
		}
	}
	if len(got) == 0 || got[len(got)-1].Text != "Editing" || !got[len(got)-1].Tool || got[len(got)-1].Recency != 0 {
		t.Fatalf("latest tool candidate: %+v", got)
	}
}

func TestCandidatesSkipNonActions(t *testing.T) {
	got := Candidates("Starting point for the proposal.\nNot in the mascot code.\nNow update the runtime.")
	if len(got) != 1 || got[0].Text != "Update the runtime" {
		t.Fatalf("non-actions became candidates: %+v", got)
	}
}

func TestCandidatesSummarizeConsecutiveScreenshots(t *testing.T) {
	got := Candidates("tool: Reading image-1.png\ntool: Reading image-2.png\ntool: Reading image-3.png")
	if len(got) != 1 || got[0].Text != "Reviewing screenshots" || !got[0].Tool {
		t.Fatalf("screenshot sequence: %+v", got)
	}
}

func TestLatestToolLabelIsVerbatim(t *testing.T) {
	text := "assistant: I am editing the panel.\ntool: Editing chat.js"
	if got := LatestToolLabel(text); got != "Editing chat.js" {
		t.Fatalf("latest tool label = %q", got)
	}
	if got := LatestToolLabel(text + "\nassistant: Checking the result."); got != "" {
		t.Fatalf("older tool label overrode newer prose: %q", got)
	}
}

func TestLatestToolLabelLeavesGenericToolsToModel(t *testing.T) {
	for _, label := range []string{"Using ToolSearch", "Using a tool", "Running tool", "Running command", "Running shell commands", "Running gh", "Running bash"} {
		if got := LatestToolLabel("assistant: Checking the result.\ntool: " + label); got != "" {
			t.Errorf("generic %q returned %q", label, got)
		}
	}
	for _, label := range []string{"Editing chat.js", "Running go test", "Running gh pr merge", "Reading chat.js", "Reviewing screenshots", "Messaging a box", "Checking contacts", "Searching code"} {
		if got := LatestToolLabel("tool: " + label); got != label {
			t.Errorf("specific %q returned %q", label, got)
		}
	}
}

func TestNormalizeActivityAcronymCasing(t *testing.T) {
	cases := []struct{ input, want string }{
		{"waiting for ci", "Waiting for CI"},
		{"waiting on pr", "Waiting on PR"},
		{"waiting for qa", "Waiting for QA"},
		{"checking api and ui", "Checking API and UI"},
		{"updating css and sso", "Updating CSS and SSO"},
		{"checking db url", "Checking DB URL"},
		{"reviewing prs", "Reviewing PRs"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if got := Normalize(tc.input); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
