package loginprofile

import (
	"encoding/json"
	"fmt"
)

// CompleteClaudeOnboarding marks Claude Code's first-run setup as done in a
// Claude profile's .claude.json. A profile signed in through the web UI comes
// from `claude auth login`, which never runs that setup, so the box's Claude
// terminal would otherwise stop at the theme picker and never take a prompt.
func CompleteClaudeOnboarding(files map[string][]byte) error {
	state := map[string]json.RawMessage{}
	if data := files[".claude.json"]; len(data) > 0 && (json.Unmarshal(data, &state) != nil || state == nil) {
		return fmt.Errorf(".claude.json is invalid; preserved unchanged")
	}
	if string(state["hasCompletedOnboarding"]) == "true" {
		return nil
	}
	state["hasCompletedOnboarding"] = json.RawMessage("true")
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("could not complete Claude onboarding")
	}
	clear(files[".claude.json"])
	files[".claude.json"] = append(encoded, '\n')
	return nil
}
