package coworker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
)

const ConfigPath = "/data/home/.vmbox-coworker/config.json"

type LaunchConfig struct {
	URL                     string `json:"url"`
	Token                   string `json:"token"`
	Agent                   string `json:"agent"`
	Prompt                  string `json:"prompt"`
	AllowDevelopmentChannel bool   `json:"allowDevelopmentChannel"`
}

func RunConfigured(ctx context.Context, input io.Reader, output, errorOutput io.Writer) error {
	data, err := os.ReadFile(ConfigPath)
	if err != nil {
		return fmt.Errorf("controller-provisioned coworker configuration missing")
	}
	defer clear(data)
	var cfg LaunchConfig
	if json.Unmarshal(data, &cfg) != nil {
		return fmt.Errorf("invalid coworker configuration")
	}
	client := Client{URL: cfg.URL, Token: cfg.Token}
	if cfg.Agent == "codex" {
		return client.Codex(ctx, "/data/home/.vmbox-coworker/codex-state.json", cfg.Prompt, output)
	}
	if cfg.Agent != "claude" || !cfg.AllowDevelopmentChannel {
		return fmt.Errorf("Claude coworker requires explicit development-channel consent")
	}
	mcp := map[string]any{"mcpServers": map[string]any{"vmbox-coworkers": map[string]any{"command": "vmbox-runtime", "args": []string{"coworker-channel"}, "env": map[string]string{"VMBOX_COWORKER_URL": cfg.URL, "VMBOX_COWORKER_TOKEN": cfg.Token}}}}
	encoded, err := json.Marshal(mcp)
	if err != nil {
		return err
	}
	defer clear(encoded)
	path := "/data/home/.vmbox-coworker/claude-mcp.json"
	if err = os.WriteFile(path, encoded, 0600); err != nil {
		return fmt.Errorf("could not write private Claude MCP configuration")
	}
	cmd := exec.CommandContext(ctx, "claude", "--mcp-config", path, "--dangerously-load-development-channels", "server:vmbox-coworkers", cfg.Prompt)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, errorOutput
	return cmd.Run()
}
