package standalone

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/loginprofile"
)

func TestPerBoxModelSelectionPreservesCopiedProviderSettings(t *testing.T) {
	t.Setenv("VMBOX_MODEL_URL", "")
	for _, tc := range []struct{ agent, dir, name, input, model, effort string }{
		{"codex", ".codex", "config.toml", "model = \"old\"\n[features]\nkeep = true\n", "gpt-6-astra", "high"},
		{"claude", ".claude", "settings.json", `{"model":"old","env":{"ANTHROPIC_API_KEY":"fixture-private"}}`, "opus", "high"},
		{"opencode", ".config/opencode", "opencode.json", `{"model":"saved/old","provider":{"saved":{"options":{"apiKey":"fixture-private"}}},"agent":{"build":{"model":"saved/old","temperature":0.5}}}`, "saved/new", "high"},
		{"opencode", ".config/opencode", "opencode.json", `{"model":"local/old","provider":{"local":{"options":{"apiKey":"fixture-private"}}}}`, "local/new", "high"},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, tc.dir)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, tc.name)
			if err := os.WriteFile(file, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			if err := applyAgentModel(home, tc.agent, tc.model, tc.effort); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if loginprofile.Model(tc.agent, map[string][]byte{tc.name: data}) != tc.model {
				t.Fatal("model override not applied")
			}
			if tc.agent != "codex" && !bytes.Contains(data, []byte("fixture-private")) {
				t.Fatal("provider credential lost")
			}
		})
	}
}

func TestAgentModelRejectsInvalidInputWithoutChangingConfiguration(t *testing.T) {
	home := t.TempDir()
	if err := applyAgentModel(home, "codex", "bad\nmodel", ""); err == nil {
		t.Fatal("control character accepted")
	}
}

func TestCustomReasoningSetsActualVariantAndInitialChatMode(t *testing.T) {
	t.Setenv("VMBOX_MODEL_URL", "http://model/v1")
	home := t.TempDir()
	if err := saveThinkingCapabilities(home, thinkingCapabilities{Efforts: []string{"low", "medium", "xhigh"}}); err != nil {
		t.Fatal(err)
	}
	if err := writeLocalModelConfig(home, "http://model/v1", "fixture", false); err != nil {
		t.Fatal(err)
	}
	if err := applyAgentModel(home, "opencode", "", "medium"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config/opencode/opencode.json")
	b, _ := os.ReadFile(path)
	var config struct {
		Agent    map[string]struct{ Model, Variant string }
		Provider map[string]struct {
			Models map[string]struct{ Variants map[string]map[string]any }
		}
	}
	if err := json.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	if config.Agent["build"].Model != "local/fixture" || config.Agent["build"].Variant != "thinking-medium" {
		t.Fatal("custom choice did not select the declared variant")
	}
	if config.Provider["local"].Models["fixture"].Variants["thinking-medium"]["reasoningEffort"] != "medium" {
		t.Fatal("variant does not forward reasoning effort")
	}
	mode, _ := os.ReadFile(thinkingPath(home))
	if !bytes.Contains(mode, []byte(`"mode":"medium"`)) {
		t.Fatal("chat thinking default lost")
	}
	if err := applyAgentModel(home, "opencode", "", "high"); err == nil {
		t.Fatal("unadvertised high effort accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(b, after) {
		t.Fatal("invalid effort changed configuration")
	}
}
