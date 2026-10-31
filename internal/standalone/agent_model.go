package standalone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	localThinking := agent == "opencode" && strings.HasPrefix(model, "local/") && effort != "" && os.Getenv("VMBOX_MODEL_URL") != ""
	if localThinking && !supportedEffort(effort, readThinkingCapabilities(home)) {
		return fmt.Errorf("model does not advertise reasoning effort %q", effort)
	}
	if !localThinking {
		if err := loginprofile.SetReasoningEffort(agent, files, effort, model); err != nil {
			return err
		}
	}
	if localThinking {
		var config map[string]any
		if json.Unmarshal(files[name], &config) != nil {
			return fmt.Errorf("invalid local model configuration")
		}
		agents, ok := config["agent"].(map[string]any)
		if config["agent"] != nil && !ok {
			return fmt.Errorf("invalid local agent configuration")
		}
		if agents == nil {
			agents = map[string]any{}
			config["agent"] = agents
		}
		build, ok := agents["build"].(map[string]any)
		if agents["build"] != nil && !ok {
			return fmt.Errorf("invalid local build configuration")
		}
		if build == nil {
			build = map[string]any{}
			agents["build"] = build
		}
		build["model"] = model
		build["variant"] = "thinking-" + effort
		files[name], _ = json.MarshalIndent(config, "", "  ")
		// The plugin also uses this private setting for subsequent web chat turns.
		if _, err := os.Stat(thinkingPath(home)); os.IsNotExist(err) {
			data, _ := json.Marshal(thinkingSetting{Mode: effort})
			if err := os.WriteFile(thinkingPath(home), data, 0600); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), files[name], 0600)
}
