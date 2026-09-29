package boxruntime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// EnsureClaudeDefaults writes the box's permissions and workspace trust into the
// box HOME. worker-agent-trust.sh writes the same settings, but into /data/home, which
// is not the HOME of a shared-worker box, so they never reached one.
func EnsureClaudeDefaults(home, workspace string) error {
	if err := ensureClaudeSettings(filepath.Join(home, ".claude", "settings.json"), workspace); err != nil {
		return err
	}
	return ensureClaudeTrust(filepath.Join(home, ".claude.json"), workspace)
}

func ensureClaudeSettings(path, workspace string) error {
	settings, err := readJSONObject(path)
	if err != nil {
		return err
	}
	permissions := map[string]any{}
	if raw, ok := settings["permissions"]; ok {
		if json.Unmarshal(raw, &permissions) != nil {
			return fmt.Errorf("invalid claude permissions; preserved unchanged")
		}
	}
	permissions["defaultMode"] = "bypassPermissions"
	settings["permissions"], _ = json.Marshal(permissions)
	// Auto mode's first-run offer can block an unattended Claude terminal even
	// though this box explicitly uses bypassPermissions. Disable that alternate
	// mode through Claude's documented setting rather than inspecting TUI text.
	settings["disableAutoMode"] = json.RawMessage(`"disable"`)
	settings["skipDangerousModePermissionPrompt"] = json.RawMessage(`true`)

	var trusted []string
	if raw, ok := settings["trustedDirectories"]; ok && json.Unmarshal(raw, &trusted) != nil {
		trusted = nil
	}
	if workspace != "" && !contains(trusted, workspace) {
		trusted = append(trusted, workspace)
	}
	settings["trustedDirectories"], _ = json.Marshal(trusted)
	return writeJSONObject(path, settings, 0600)
}

func ensureClaudeTrust(path, workspace string) error {
	if workspace == "" {
		return nil
	}
	state, err := readJSONObject(path)
	if err != nil {
		return err
	}
	projects := map[string]json.RawMessage{}
	if raw, ok := state["projects"]; ok {
		if json.Unmarshal(raw, &projects) != nil {
			return fmt.Errorf("invalid claude project state; preserved unchanged")
		}
	}
	entry := map[string]any{}
	if raw, ok := projects[workspace]; ok {
		if json.Unmarshal(raw, &entry) != nil {
			entry = map[string]any{}
		}
	}
	entry["hasTrustDialogAccepted"] = true
	projects[workspace], _ = json.Marshal(entry)
	state["projects"], _ = json.Marshal(projects)
	return writeJSONObject(path, state, 0600)
}

// readJSONObject reads a JSON object, treating an absent file as empty. A file
// that will not parse is an error rather than something to overwrite: it is the
// user's configuration.
func readJSONObject(path string) (map[string]json.RawMessage, error) {
	value := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return value, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return value, nil
	}
	if json.Unmarshal(data, &value) != nil || value == nil {
		return nil, fmt.Errorf("invalid JSON in %s; preserved unchanged", filepath.Base(path))
	}
	return value, nil
}

func writeJSONObject(path string, value map[string]json.RawMessage, mode os.FileMode) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeTextAtomic(path, string(encoded)+"\n", mode)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
