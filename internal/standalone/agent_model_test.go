package standalone

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/loginprofile"
)

func TestPerBoxModelSelectionPreservesCopiedProviderSettings(t *testing.T) {
	for _, tc := range []struct{ agent, dir, name, input, model, effort string }{
		{"codex", ".codex", "config.toml", "model = \"old\"\n[features]\nkeep = true\n", "gpt-6-astra", "high"},
		{"claude", ".claude", "settings.json", `{"model":"old","env":{"ANTHROPIC_API_KEY":"fixture-private"}}`, "opus", "high"},
		{"opencode", ".config/opencode", "opencode.json", `{"model":"saved/old","provider":{"saved":{"options":{"apiKey":"fixture-private"}}},"agent":{"build":{"model":"saved/old","temperature":0.5}}}`, "saved/new", "high"},
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
