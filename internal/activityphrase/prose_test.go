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

func TestExplicitIntentActivity(t *testing.T) {
	cases := map[string]string{
		"assistant: I'll inspect sidebar spacing next.":       "Preparing to inspect sidebar",
		"assistant: Let's check the worker logs now.":         "Preparing to check worker logs",
		"assistant: We will review CSS, then send an update.": "Preparing to review CSS",
		"assistant: Builder will inspect the sidebar.":        "",
		"assistant: I fixed the layout yesterday.":            "",
		"assistant: I'll inspect CSS.\ntool: Using a tool":    "",
	}
	for input, want := range cases {
		if got := ExplicitIntentActivity(input); got != want {
			t.Errorf("%q: got %q, want %q", input, got, want)
		}
	}
}

func TestExplicitProseActivityCompletesItsObject(t *testing.T) {
	cases := []struct{ input, want string }{
		{"assistant: I'm asking builder if there's time to review.", "Asking builder if there's time"},
		{"assistant: I'm asking builder if there's.", "Asking builder"},
		{"assistant: I'm telling owner they are ready.", "Telling owner they are ready"},
		{"assistant: I'm telling owner they are.", "Telling owner"},
		{"assistant: I'm waiting on the other.", ""},
		{"assistant: I'm marking as the top priority.", "Marking as the top priority"},
		{"assistant: I'm marking as the top.", ""},
		{"assistant: I'm sending builder a speed limit.", "Sending builder a speed limit"},
		{"assistant: I'm sending builder a speed.", "Sending builder"},
		{"assistant: I'm leaving until the owner returns.", "Leaving until the owner returns"},
		{"assistant: I'm leaving until the owner.", ""},
		{"assistant: I'm starting with step 2.", "Starting with step 2"},
		{"assistant: I'm starting with step.", ""},
		{"assistant: I'm waiting for your next.", ""},
		{"assistant: I'm waiting for it.", ""},
		{"assistant: I'm waiting for the CI results.", "Waiting for the CI results"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if got := ExplicitProseActivity(tc.input); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCompletedAndWaitingForWork(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"assistant: Done. Waiting for your next task.", true},
		{"assistant: The build is finished. I'm waiting for instructions.", true},
		{"assistant: Finished the report.\nassistant: Waiting for your next message.", true},
		{"assistant: Done. Waiting for approval to merge.", false},
		{"assistant: Waiting for your next task.", false},
		{"assistant: Done. Waiting for your next task.\ntool: Running go test", false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if got := CompletedAndWaitingForWork(tc.input); got != tc.want {
				t.Errorf("got %t, want %t", got, tc.want)
			}
		})
	}
}
