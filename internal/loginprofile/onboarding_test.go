package loginprofile

import (
	"encoding/json"
	"testing"
)

func TestCompleteClaudeOnboarding(t *testing.T) {
	for name, input := range map[string]string{
		"missing file":   "",
		"login state":    `{"oauthAccount":{"emailAddress":"a@example.com"},"projects":{"/w":{"hasTrustDialogAccepted":true}}}`,
		"explicit false": `{"hasCompletedOnboarding":false,"theme":"light"}`,
		"already set":    `{"hasCompletedOnboarding":true,"theme":"light"}`,
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string][]byte{}
			if input != "" {
				files[".claude.json"] = []byte(input)
			}
			if err := CompleteClaudeOnboarding(files); err != nil {
				t.Fatal(err)
			}
			var state map[string]any
			if err := json.Unmarshal(files[".claude.json"], &state); err != nil {
				t.Fatal(err)
			}
			if state["hasCompletedOnboarding"] != true {
				t.Fatalf("onboarding not completed: %s", files[".claude.json"])
			}
			var original map[string]any
			if input != "" {
				_ = json.Unmarshal([]byte(input), &original)
			}
			for key, value := range original {
				if key != "hasCompletedOnboarding" && !jsonEqual(state[key], value) {
					t.Fatalf("%s changed: %v", key, state[key])
				}
			}
		})
	}
	files := map[string][]byte{".claude.json": []byte("{not json")}
	if err := CompleteClaudeOnboarding(files); err == nil || string(files[".claude.json"]) != "{not json" {
		t.Fatalf("invalid state was not preserved: %v %q", err, files[".claude.json"])
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
