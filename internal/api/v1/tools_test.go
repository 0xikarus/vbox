package v1

import "testing"

func TestBlenderPreset(t *testing.T) {
	if err := ValidateTools([]string{"desktop", "foundry", "blender"}); err != nil {
		t.Fatal(err)
	}
	for _, tools := range [][]string{{"blender", "blender"}, {"desktop", "desktop"}, {"unknown"}} {
		if ValidateTools(tools) == nil {
			t.Fatalf("accepted invalid tools: %v", tools)
		}
	}
	for _, preset := range ToolPresets() {
		if preset.ID == "blender" {
			if preset.Version != "5.1.2 + Blender MCP 1.9.1" {
				t.Fatalf("Blender MCP version is not visible or pinned: %q", preset.Version)
			}
			return
		}
	}
	t.Fatal("Blender missing from UI catalog")
}
