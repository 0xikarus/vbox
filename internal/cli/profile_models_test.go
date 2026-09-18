package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyProfileModelPreservesClaudeAndCodexConfiguration(t *testing.T) {
	claude := map[string][]byte{"settings.json": []byte(`{"permissions":{"defaultMode":"bypassPermissions"},"model":"old"}`)}
	if err := applyProfileModel("claude", claude, "sonnet"); err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Model       string `json:"model"`
		Permissions struct {
			DefaultMode string `json:"defaultMode"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(claude["settings.json"], &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Model != "sonnet" || settings.Permissions.DefaultMode != "bypassPermissions" {
		t.Fatalf("Claude settings=%s", claude["settings.json"])
	}

	codex := map[string][]byte{"config.toml": []byte("check_for_update_on_startup = false\nmodel = \"old\"\n\n[projects.\"/workspace\"]\ntrust_level = \"trusted\"\n")}
	if err := applyProfileModel("codex", codex, "gpt-test"); err != nil {
		t.Fatal(err)
	}
	config := string(codex["config.toml"])
	if !strings.Contains(config, `model = "gpt-test"`) || strings.Count(config, "model =") != 1 || !strings.Contains(config, `[projects."/workspace"]`) {
		t.Fatalf("Codex config=%q", config)
	}
}

func TestDetectedProfileModel(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"model":"sonnet"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := detectedProfileModel("claude", root); got != "sonnet" {
		t.Fatal(got)
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("model = 'gpt-test'\n[projects.x]\nmodel = \"nested\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := detectedProfileModel("codex", root); got != "gpt-test" {
		t.Fatal(got)
	}
}

func TestApplyProfileModelAddsTopLevelCodexModelBeforeTables(t *testing.T) {
	files := map[string][]byte{"config.toml": []byte("[projects.x]\ntrust_level = \"trusted\"\n")}
	if err := applyProfileModel("codex", files, "gpt-test"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(files["config.toml"]), "model = \"gpt-test\"\n[projects.x]") {
		t.Fatalf("Codex config=%q", files["config.toml"])
	}
}
