package boxruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureClaudeDefaultsGrantsPermissionsAndTrust(t *testing.T) {
	home := t.TempDir()
	workspace := "/data/workspaces/box/workspace"
	if err := EnsureClaudeDefaults(home, workspace); err != nil {
		t.Fatal(err)
	}
	settings := readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	if mode := settings["permissions"].(map[string]any)["defaultMode"]; mode != "bypassPermissions" {
		t.Fatalf("defaultMode = %v", mode)
	}
	if got := settings["disableAutoMode"]; got != "disable" {
		t.Fatalf("auto mode was not disabled: %v", got)
	}
	if got := settings["skipDangerousModePermissionPrompt"]; got != true {
		t.Fatalf("bypass confirmation was not disabled: %v", got)
	}
	if dirs := settings["trustedDirectories"].([]any); len(dirs) != 1 || dirs[0] != workspace {
		t.Fatalf("trustedDirectories = %v", dirs)
	}
	state := readJSON(t, filepath.Join(home, ".claude.json"))
	entry := state["projects"].(map[string]any)[workspace].(map[string]any)
	if entry["hasTrustDialogAccepted"] != true {
		t.Fatalf("trust dialog not accepted: %v", entry)
	}
	if state["hasCompletedOnboarding"] != true {
		t.Fatalf("first-run setup would block the terminal: %v", state)
	}
}

// An imported login profile brings its own settings; the box's permissions are
// added without discarding what the user configured.
func TestEnsureClaudeDefaultsPreservesExistingSettings(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	existing := `{"model":"sonnet","permissions":{"allow":["Bash(ls:*)"],"defaultMode":"acceptEdits"},"trustedDirectories":["/home/user/other"]}`
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := "/data/workspaces/box/workspace"
	if err := EnsureClaudeDefaults(home, workspace); err != nil {
		t.Fatal(err)
	}
	settings := readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	if settings["model"] != "sonnet" {
		t.Fatalf("unrelated setting lost: %v", settings)
	}
	permissions := settings["permissions"].(map[string]any)
	if permissions["defaultMode"] != "bypassPermissions" {
		t.Fatalf("defaultMode not raised: %v", permissions)
	}
	if allow := permissions["allow"].([]any); len(allow) != 1 || allow[0] != "Bash(ls:*)" {
		t.Fatalf("existing allow list lost: %v", permissions)
	}
	dirs := settings["trustedDirectories"].([]any)
	if len(dirs) != 2 || dirs[0] != "/home/user/other" || dirs[1] != workspace {
		t.Fatalf("trustedDirectories = %v", dirs)
	}
}

func TestEnsureClaudeDefaultsIsIdempotent(t *testing.T) {
	home := t.TempDir()
	workspace := "/data/workspaces/box/workspace"
	for range 3 {
		if err := EnsureClaudeDefaults(home, workspace); err != nil {
			t.Fatal(err)
		}
	}
	settings := readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	if dirs := settings["trustedDirectories"].([]any); len(dirs) != 1 {
		t.Fatalf("workspace trusted %d times: %v", len(dirs), dirs)
	}
}

// Unparseable configuration is the user's; it must be reported, not replaced.
func TestEnsureClaudeDefaultsRefusesInvalidSettings(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureClaudeDefaults(home, "/data/workspaces/box/workspace"); err == nil {
		t.Fatal("invalid settings must be reported")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{not json" {
		t.Fatalf("settings were modified: %q %v", data, err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{}
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return value
}
