package loginprofile

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Model returns the non-secret model default stored in a portable agent
// profile. Unknown or malformed configuration is reported as no default; the
// credential itself is still handled by Validate.
func Model(application string, files map[string][]byte) string {
	switch application {
	case "claude":
		return jsonModel(files["settings.json"])
	case "codex":
		return topLevelTOMLString(files["config.toml"], "model")
	case "opencode":
		if data := files["opencode.json"]; len(data) > 0 {
			return jsonModel(data)
		}
		return jsonModel(normalizeJSONC(files["opencode.jsonc"]))
	default:
		return ""
	}
}

// SetModel writes a per-box model override into a copied profile without
// changing the encrypted account profile it came from.
func SetModel(application string, files map[string][]byte, model string) error {
	model = strings.TrimSpace(model)
	if model == "" || len(model) > 200 {
		return fmt.Errorf("choose a model name up to 200 characters")
	}
	for _, r := range model {
		if unicode.IsControl(r) {
			return fmt.Errorf("model name cannot contain control characters")
		}
	}
	switch application {
	case "claude":
		return setJSONModel(files, "settings.json", model, false)
	case "codex":
		files["config.toml"] = setTopLevelTOMLString(files["config.toml"], "model", model)
		return nil
	case "opencode":
		name := "opencode.json"
		jsonc := false
		if len(files[name]) == 0 && len(files["opencode.jsonc"]) > 0 {
			name, jsonc = "opencode.jsonc", true
		}
		return setJSONModel(files, name, model, jsonc)
	default:
		return fmt.Errorf("model selection is supported for Claude, Codex, and OpenCode profiles")
	}
}

// SetReasoningEffort writes a box-specific setting to the copied profile. An
// empty choice leaves the uploaded profile default alone. OpenCode calls this
// setting a model variant; its availability is provider/model-specific.
func SetReasoningEffort(application string, files map[string][]byte, effort, model string) error {
	if effort == "" {
		return nil
	}
	if model == "" {
		return fmt.Errorf("choose a model before choosing reasoning effort")
	}
	allowed := map[string]bool{}
	switch application {
	case "codex":
		allowed = map[string]bool{"minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	case "claude":
		if strings.Contains(strings.ToLower(model), "haiku") {
			return fmt.Errorf("Claude Haiku does not support reasoning effort")
		}
		// Claude Code does not accept max in effortLevel settings. That level
		// requires a session flag or environment override instead.
		allowed = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true}
	case "opencode":
		allowed = map[string]bool{"none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	default:
		return fmt.Errorf("reasoning effort is supported for Claude, Codex, and OpenCode profiles")
	}
	if !allowed[effort] {
		return fmt.Errorf("unsupported %s reasoning effort %q", application, effort)
	}
	switch application {
	case "codex":
		files["config.toml"] = setTopLevelTOMLString(files["config.toml"], "model_reasoning_effort", effort)
		return nil
	case "claude":
		return setJSONField(files, "settings.json", "effortLevel", effort, false)
	case "opencode":
		name := "opencode.json"
		jsonc := false
		if len(files[name]) == 0 && len(files["opencode.jsonc"]) > 0 {
			name, jsonc = "opencode.jsonc", true
		}
		data := files[name]
		if jsonc {
			data = normalizeJSONC(data)
		}
		config := map[string]json.RawMessage{}
		if len(data) > 0 && (json.Unmarshal(data, &config) != nil || config == nil) {
			return fmt.Errorf("%s is invalid; preserved unchanged", name)
		}
		var agents map[string]json.RawMessage
		if len(config["agent"]) > 0 && (json.Unmarshal(config["agent"], &agents) != nil || agents == nil) {
			return fmt.Errorf("%s agent configuration is invalid; preserved unchanged", name)
		}
		if agents == nil {
			agents = map[string]json.RawMessage{}
		}
		var build map[string]json.RawMessage
		if len(agents["build"]) > 0 && (json.Unmarshal(agents["build"], &build) != nil || build == nil) {
			return fmt.Errorf("%s build agent configuration is invalid; preserved unchanged", name)
		}
		if build == nil {
			build = map[string]json.RawMessage{}
		}
		build["model"], _ = json.Marshal(model)
		build["variant"], _ = json.Marshal(effort)
		agents["build"], _ = json.Marshal(build)
		config["agent"], _ = json.Marshal(agents)
		encoded, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return fmt.Errorf("could not set OpenCode model variant")
		}
		files[name] = append(encoded, '\n')
		return nil
	}
	return nil
}

func jsonModel(data []byte) string {
	var config struct {
		Model string `json:"model"`
	}
	if len(data) == 0 || json.Unmarshal(data, &config) != nil {
		return ""
	}
	return strings.TrimSpace(config.Model)
}

func setJSONModel(files map[string][]byte, name, model string, jsonc bool) error {
	return setJSONField(files, name, "model", model, jsonc)
}

func setJSONField(files map[string][]byte, name, field, value string, jsonc bool) error {
	config := map[string]json.RawMessage{}
	data := files[name]
	if jsonc {
		data = normalizeJSONC(data)
	}
	if len(data) > 0 && (json.Unmarshal(data, &config) != nil || config == nil) {
		return fmt.Errorf("%s is invalid; preserved unchanged", name)
	}
	config[field], _ = json.Marshal(value)
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("could not set the agent model")
	}
	files[name] = append(encoded, '\n')
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

// normalizeJSONC removes comments and trailing commas while retaining quoted
// strings. OpenCode accepts JSONC; converting a copied profile to strict JSON
// lets us update its model without adding a parser dependency.
func normalizeJSONC(data []byte) []byte {
	var out []byte
	inString, escaped, lineComment, blockComment := false, false, false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if lineComment {
			if c == '\n' {
				lineComment = false
				out = append(out, c)
			}
			continue
		}
		if blockComment {
			if c == '*' && i+1 < len(data) && data[i+1] == '/' {
				blockComment = false
				i++
			} else if c == '\n' {
				out = append(out, c)
			}
			continue
		}
		if inString {
			out = append(out, c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(data) {
			switch data[i+1] {
			case '/':
				lineComment = true
				i++
				continue
			case '*':
				blockComment = true
				i++
				continue
			}
		}
		out = append(out, c)
	}
	clean := make([]byte, 0, len(out))
	inString, escaped = false, false
	for i, c := range out {
		if inString {
			clean = append(clean, c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			clean = append(clean, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(out) && unicode.IsSpace(rune(out[j])) {
				j++
			}
			if j < len(out) && (out[j] == '}' || out[j] == ']') {
				continue
			}
		}
		clean = append(clean, c)
	}
	return clean
}
