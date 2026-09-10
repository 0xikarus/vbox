package v1

import "testing"

func TestBlenderPreset(t *testing.T) {
	if err := ValidateTools([]string{"foundry", "blender"}); err != nil {
		t.Fatal(err)
	}
	for _, tools := range [][]string{{"blender", "blender"}, {"unknown"}} {
		if ValidateTools(tools) == nil {
			t.Fatalf("accepted invalid tools: %v", tools)
		}
	}
	for _, preset := range ToolPresets() {
		if preset.ID == "blender" {
			return
		}
	}
	t.Fatal("Blender missing from UI catalog")
}
