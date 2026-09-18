package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

func detectedProfileModel(application, root string) string {
	switch application {
	case "claude":
		data, err := os.ReadFile(filepath.Join(root, "settings.json"))
		if err != nil {
			return ""
		}
		var settings struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(data, &settings) != nil {
			return ""
		}
		return strings.TrimSpace(settings.Model)
	case "codex":
		data, err := os.ReadFile(filepath.Join(root, "config.toml"))
		if err != nil {
			return ""
		}
		return topLevelTOMLString(data, "model")
	default:
		return ""
	}
}

func applyProfileModel(application string, files map[string][]byte, model string) error {
	model = strings.TrimSpace(model)
	if model == "" || len(model) > 200 {
		return fmt.Errorf("enter a model name up to 200 characters")
	}
	for _, r := range model {
		if unicode.IsControl(r) {
			return fmt.Errorf("model name cannot contain control characters")
		}
	}
	switch application {
	case "claude":
		settings := map[string]json.RawMessage{}
		if data := files["settings.json"]; len(data) > 0 && (json.Unmarshal(data, &settings) != nil || settings == nil) {
			return fmt.Errorf("Claude settings.json is invalid; preserved unchanged")
		}
		settings["model"], _ = json.Marshal(model)
		data, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return fmt.Errorf("could not set the Claude model")
		}
		files["settings.json"] = append(data, '\n')
	case "codex":
		files["config.toml"] = setTopLevelTOMLString(files["config.toml"], "model", model)
	default:
		return fmt.Errorf("model selection is supported for Claude and Codex profiles")
	}
	return nil
}

func topLevelTOMLString(data []byte, key string) string {
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			break
		}
		left, right, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimSpace(left) != key {
			continue
		}
		value := strings.TrimSpace(strings.SplitN(right, "#", 2)[0])
		if decoded, err := strconv.Unquote(value); err == nil {
			return strings.TrimSpace(decoded)
		}
		if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			return strings.TrimSpace(value[1 : len(value)-1])
		}
	}
	return ""
}

func setTopLevelTOMLString(data []byte, key, value string) []byte {
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	replacement := key + " = " + strconv.Quote(value)
	insertAt := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			insertAt = i
			break
		}
		left, _, ok := strings.Cut(trimmed, "=")
		if ok && strings.TrimSpace(left) == key {
			lines[i] = replacement
			return []byte(strings.Join(lines, "\n") + "\n")
		}
	}
	lines = append(lines, "")
	copy(lines[insertAt+1:], lines[insertAt:])
	lines[insertAt] = replacement
	return []byte(strings.Join(lines, "\n") + "\n")
}
