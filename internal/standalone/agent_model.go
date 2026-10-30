package standalone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/0xikarus/vmbox-service/internal/loginprofile"
)

// Override only this box's copied configuration, retaining the reusable profile.
func applyAgentModel(home, agent, model, effort string) error {
	if model == "" && effort == "" {
		return nil
	}
	root, name := "."+agent, "settings.json"
	if agent == "codex" {
		name = "config.toml"
	}
	if agent == "opencode" {
		root = filepath.Join(".config", "opencode")
		name = "opencode.json"
	}
	dir := filepath.Join(home, root)
	files := map[string][]byte{}
	if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
		files[name] = data
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cannot read copied agent settings")
	}
	if model != "" {
		if err := loginprofile.SetModel(agent, files, model); err != nil {
			return err
		}
		if agent == "opencode" {
			var config map[string]any
			if json.Unmarshal(files[name], &config) == nil {
				if agents, ok := config["agent"].(map[string]any); ok {
					if build, ok := agents["build"].(map[string]any); ok {
						build["model"] = model
						files[name], _ = json.MarshalIndent(config, "", "  ")
					}
				}
			}
		}
	}
	if model == "" {
		model = loginprofile.Model(agent, files)
	}
	if err := loginprofile.SetReasoningEffort(agent, files, effort, model); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), files[name], 0600)
}
