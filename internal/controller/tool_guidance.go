package controller

import (
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// The selected presets are installed before a new box becomes usable. Persist
// this generated text separately from the user's preset snapshot, so editing
// box instructions later cannot silently remove or duplicate it. Imported and
// pre-existing boxes get no generated guidance.
func newBoxToolGuidance(tools []string) string {
	selected := make(map[string]bool, len(tools))
	for _, tool := range tools {
		selected[tool] = true
	}
	var lines []string
	if selected["desktop"] || selected["blender"] {
		lines = append(lines, "- Chromium: `chromium` · profile `~/.config/vmbox/chromium` · display `:99`.")
	}
	if selected["blender"] {
		lines = append(lines, "- Blender: `~/bin/blender` · Blender MCP.")
	}
	if selected["foundry"] {
		lines = append(lines, "- Foundry: `~/bin/forge`, `~/bin/cast`, `~/bin/anvil`, and `~/bin/chisel`.")
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Available box tools\n\n" + strings.Join(lines, "\n") + "\n"
}

func composeInstructionMarkdown(user, guidance string) (string, error) {
	if guidance == "" {
		return user, v1.ValidateInstructionMarkdown(user)
	}
	combined := user
	if combined != "" {
		if !strings.HasSuffix(combined, "\n") {
			combined += "\n"
		}
		combined += "\n"
	}
	combined += guidance
	return combined, v1.ValidateInstructionMarkdown(combined)
}
