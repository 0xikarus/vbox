package boxruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRegisterOpenCodeTUIPreservesSettings(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".config")
	configPath := filepath.Join(root, "opencode", "tui.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"theme":"existing","plugin":["custom-plugin"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := registerOpenCodeTUI(home, root); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Theme   string   `json:"theme"`
		Plugins []string `json:"plugin"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Theme != "existing" || len(config.Plugins) != 2 || config.Plugins[0] != "custom-plugin" {
		t.Fatalf("settings changed or plugin duplicated: %s", data)
	}
	if _, err := os.Stat(config.Plugins[1]); err != nil {
		t.Fatal(err)
	}
}
