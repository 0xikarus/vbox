package standalone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Preserve imported model capabilities and overrides across controller restarts.
// Unknown custom models are text-only unless image input is explicitly enabled.
func writeLocalModelConfig(home, endpoint, model string, vision bool) error {
	if model == "" {
		return fmt.Errorf("VMBOX_MODEL is required with VMBOX_MODEL_URL")
	}
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	config := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(b, &config) != nil || config == nil {
			return fmt.Errorf("invalid imported OpenCode JSON")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	object := func(parent map[string]any, key string) map[string]any {
		m, _ := parent[key].(map[string]any)
		if m == nil {
			m = map[string]any{}
			parent[key] = m
		}
		return m
	}
	config["model"] = "local/" + model
	local := object(object(config, "provider"), "local")
	local["npm"] = "@ai-sdk/openai-compatible"
	local["name"] = "Local model"
	object(local, "options")["baseURL"] = endpoint
	entry := object(object(local, "models"), model)
	entry["name"] = model
	caps := readThinkingCapabilities(home)
	configureThinking(entry, caps)
	if caps.Context > 0 {
		limits := object(entry, "limit")
		if _, configured := limits["context"]; !configured {
			limits["context"] = caps.Context
		}
		// Reserve bounded generation space so OpenCode can compact before a
		// long vision/tool conversation exceeds the backend's actual context.
		// Explicit imported owner limits always take precedence.
		if _, configured := limits["output"]; !configured {
			limits["output"] = min(8192, caps.Context/4)
		}
	}
	if vision {
		modalities := object(entry, "modalities")
		inputs, _ := modalities["input"].([]any)
		for _, required := range []string{"text", "image"} {
			found := false
			for _, v := range inputs {
				if v == required {
					found = true
				}
			}
			if !found {
				inputs = append(inputs, required)
			}
		}
		modalities["input"] = inputs
		if _, ok := modalities["output"]; !ok {
			modalities["output"] = []string{"text"}
		}
	}
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}
