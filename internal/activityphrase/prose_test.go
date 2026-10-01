package activityphrase

import "testing"

func TestExplicitProseActivity(t *testing.T) {
	cases := map[string]string{
		"assistant: I'm sending it the pair-view fixes and giving Builder a note.": "Sending pair-view fixes",
		"assistant: Pair-view merged. Now rebasing the tooltip branch onto main.":  "Rebasing tooltip branch",
		"assistant: I'm running the full browser suite in the background.":         "Running full browser suite",
		"assistant: I'm checking that its agent is ready.":                         "",
		"assistant: Builder is sending the patch and reviewing its tests.":         "",
		"assistant: I'm fixing the layout.\ntool: Using a tool":                    "Fixing layout",
	}
	for input, want := range cases {
		if got := ExplicitProseActivity(input); got != want {
			t.Errorf("%q: got %q, want %q", input, got, want)
		}
	}
}
