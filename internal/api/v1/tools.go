package v1

import (
	"fmt"
	"strings"
)

func ValidateSetupScript(script string) error {
	if len(script) > 32768 || strings.ContainsRune(script, 0) {
		return fmt.Errorf("custom install commands must be at most 32 KiB and contain no NUL bytes")
	}
	return nil
}

// Tool presets are provider-independent, explicitly selected, and versioned.
type ToolPreset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

func ToolPresets() []ToolPreset {
	return []ToolPreset{{ID: "foundry", Name: "Foundry", Version: "v1.8.1", Description: "forge, cast, anvil and chisel · about 500 MiB installed"}}
}
func ValidateTools(tools []string) error {
	seen := map[string]bool{}
	for _, tool := range tools {
		if tool != "foundry" || seen[tool] {
			return fmt.Errorf("select each supported tool preset at most once (foundry)")
		}
		seen[tool] = true
	}
	return nil
}
