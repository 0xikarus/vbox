package cli

import (
	"fmt"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"slices"
)

func toolFields(selected []string) []*formField {
	var fields []*formField
	for _, preset := range v1.ToolPresets() {
		f := &formField{Label: preset.ID, Value: "Skip", Choices: []string{"Skip", "Install"}, Checkbox: true}
		if slices.Contains(selected, preset.ID) {
			f.Value = "Install"
		}
		f.RenderRow = func(width int) string {
			mark := "[ ]"
			if f.Value == "Install" {
				mark = "[x]"
			}
			return fmt.Sprintf("%s %s %s · %s", mark, preset.Name, preset.Version, preset.Description)
		}
		fields = append(fields, f)
	}
	return fields
}
func selectedTools(fields []*formField) []string {
	var tools []string
	for _, f := range fields {
		if f.Value == "Install" {
			tools = append(tools, f.Label)
		}
	}
	return tools
}
