package activityphrase

import (
	"encoding/json"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

// NormalizeEvidence brings older transcript excerpts and current heartbeat
// evidence to the same short tool labels before training and inference. It is
// safe to call again on evidence that has already been normalized.
func NormalizeEvidence(text string) string {
	var lines []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if label, ok := strings.CutPrefix(line, "tool: "); ok {
			label = normalizeEvidenceTool(label)
			if label == "" {
				continue
			}
			line = "tool: " + label
		}
		lines = append(lines, line)
	}
	var normalized []string
	for i := 0; i < len(lines); {
		if activityImageTool.MatchString(lines[i]) {
			end := i + 1
			for end < len(lines) && activityImageTool.MatchString(lines[end]) {
				end++
			}
			if end-i >= 2 {
				normalized = append(normalized, "tool: Reviewing screenshots")
				i = end
				continue
			}
		}
		if len(normalized) == 0 || !strings.HasPrefix(lines[i], "tool: ") || lines[i] != normalized[len(normalized)-1] {
			normalized = append(normalized, lines[i])
		}
		i++
	}
	return strings.Join(normalized, "\n")
}

func normalizeEvidenceTool(label string) string {
	if label == "Running tool" || label == "Running command" {
		return ""
	}
	if command, ok := strings.CutPrefix(label, "Running "); ok {
		if command == "node tests" {
			return label
		}
		input, _ := json.Marshal(map[string]string{"command": command})
		parsed := boxruntime.MascotToolLabel("Bash", input)
		if parsed == "Running command" {
			return ""
		}
		return parsed
	}
	if name, ok := strings.CutPrefix(label, "Using "); ok {
		if name == "[redacted]" || name == "a tool" {
			return "Using a tool"
		}
		return boxruntime.MascotToolLabel(name, nil)
	}
	return label
}
