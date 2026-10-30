package standalone

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalModelCapabilitiesSurviveRestart(t *testing.T) {
	home := t.TempDir()
	if err := writeLocalModelConfig(home, "http://model/v1", "vision", true); err != nil {
		t.Fatal(err)
	}
	// A later boot without the flag must retain the explicit capability.
	if err := writeLocalModelConfig(home, "http://model/v1", "vision", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".config/opencode/opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Provider map[string]struct {
			Models map[string]struct{ Modalities struct{ Input []string } }
		}
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	input := c.Provider["local"].Models["vision"].Modalities.Input
	if len(input) != 2 || input[0] != "text" || input[1] != "image" {
		t.Fatalf("lost image capability: %v", input)
	}
	if err := writeLocalModelConfig(home, "http://model/v1", "text-only", false); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(home, ".config/opencode/opencode.json"))
	json.Unmarshal(b, &c)
	if len(c.Provider["local"].Models["text-only"].Modalities.Input) != 0 {
		t.Fatal("unknown model advertised vision")
	}
}

func TestLocalModelUsesAdvertisedContextAndRetainsOwnerLimits(t *testing.T) {
	home := t.TempDir()
	if err := saveThinkingCapabilities(home, thinkingCapabilities{Context: 65536}); err != nil {
		t.Fatal(err)
	}
	if err := writeLocalModelConfig(home, "http://model/v1", "vision", true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config/opencode/opencode.json")
	b, _ := os.ReadFile(path)
	var config map[string]any
	if err := json.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	entry := config["provider"].(map[string]any)["local"].(map[string]any)["models"].(map[string]any)["vision"].(map[string]any)
	limits := entry["limit"].(map[string]any)
	if limits["context"] != float64(65536) || limits["output"] != float64(8192) {
		t.Fatal("backend context/output budget missing")
	}
	limits["context"] = 32768
	limits["output"] = 4096
	b, _ = json.Marshal(config)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeLocalModelConfig(home, "http://model/v1", "vision", true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if err := json.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	entry = config["provider"].(map[string]any)["local"].(map[string]any)["models"].(map[string]any)["vision"].(map[string]any)
	limits = entry["limit"].(map[string]any)
	if limits["context"] != float64(32768) || limits["output"] != float64(4096) {
		t.Fatal("owner limits overwritten")
	}
}
