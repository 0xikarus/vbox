package boxruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const managedDesktopRuntimePath = "/data/home/bin/vmbox-runtime"

// RegisterDesktopMCP registers only the selected client, before starting it.
// Existing entries are never overwritten. Diagnostics omit configuration output
// because unrelated MCP entries may contain credentials.
// desktopMCPEnvironment is what `vmbox-runtime desktop-mcp` needs to find its
// box: the workspace root and the tmux server holding the assignment fence.
var desktopMCPEnvironment = []string{"HOME", "VMBOX_WORKSPACE_ROOT", "VMBOX_RUNTIME_DIR", "TMUX_TMPDIR", "XDG_RUNTIME_DIR", "VMBOX_DESKTOP_DISPLAY"}

func RegisterDesktopMCP(ctx context.Context, home, agent string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if agent == "shell" {
		return nil
	}
	if agent != "codex" && agent != "claude" && agent != "opencode" {
		return fmt.Errorf("unsupported desktop agent")
	}
	lockDir := filepath.Join(home, ".config", "vmbox")
	if err := os.MkdirAll(lockDir, 0700); err != nil {
		return err
	}
	if err := writeDesktopMCPGuide(home); err != nil {
		return fmt.Errorf("write desktop MCP guide: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(lockDir, "mcp-registration.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if err != unix.EWOULDBLOCK {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if agent == "opencode" {
		return registerOpenCodeDesktop(home)
	}
	path, err := exec.LookPath(agent)
	if err != nil {
		return fmt.Errorf("selected agent is not installed")
	}
	switch agent {
	case "codex":
		// Re-asserted on every attach: an imported login profile ships its own
		// config.toml, which would otherwise drop the box's permissions.
		if err := EnsureCodexDefaults(home, WorkspaceDirectory()); err != nil {
			return fmt.Errorf("could not apply codex box defaults: %w", err)
		}
	case "claude":
		if err := EnsureClaudeDefaults(home, WorkspaceDirectory()); err != nil {
			return fmt.Errorf("could not apply claude box defaults: %w", err)
		}
	}
	probe := exec.CommandContext(ctx, path, "mcp", "get", "vmbox-desktop")
	probe.Env = append(os.Environ(), "HOME="+home)
	output, err := probe.CombinedOutput()
	if err == nil {
		lower := strings.ToLower(string(output))
		legacy := strings.Contains(lower, "command: vmbox-runtime") && strings.Contains(lower, "args: desktop-mcp") && !strings.Contains(lower, desktopRuntimePath())
		if !legacy {
			return nil
		}
		removeArgs := []string{"mcp", "remove", "vmbox-desktop"}
		if agent == "claude" {
			removeArgs = append(removeArgs, "--scope", "user")
		}
		remove := exec.CommandContext(ctx, path, removeArgs...)
		remove.Env = probe.Env
		if err := remove.Run(); err != nil {
			return fmt.Errorf("could not migrate %s desktop MCP", agent)
		}
	}
	if err != nil && !strings.Contains(strings.ToLower(string(output)), "no mcp server named") {
		return fmt.Errorf("could not inspect %s desktop MCP configuration", agent)
	}
	args := []string{"mcp", "add", "vmbox-desktop"}
	if agent == "claude" {
		args = append(args, "--scope", "user")
	}
	// Codex launches MCP servers with a sanitized environment, so the desktop
	// server has to be told where its workspace and tmux server are. Without
	// them it exits immediately with "worker assignment unavailable" and Codex
	// reports "MCP startup incomplete (failed: vmbox-desktop)", leaving the agent
	// with no chat_message tool to answer on. OpenCode and Claude inherit the
	// environment instead, which is why only Codex loses the tools.
	if agent == "codex" {
		for _, key := range desktopMCPEnvironment {
			if value := os.Getenv(key); value != "" {
				args = append(args, "--env", key+"="+value)
			}
		}
	}
	args = append(args, "--", desktopRuntimePath(), "desktop-mcp")
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = probe.Env
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("could not register %s desktop MCP", agent)
	}
	return nil
}

func registerOpenCodeDesktop(home string) error {
	configRoot := os.Getenv("XDG_CONFIG_HOME")
	if configRoot == "" {
		configRoot = filepath.Join(home, ".config")
	}
	if err := registerOpenCodeTUI(home, configRoot); err != nil {
		return err
	}
	dir := filepath.Join(configRoot, "opencode")
	path := filepath.Join(dir, "opencode.json")
	if custom := os.Getenv("OPENCODE_CONFIG"); custom != "" {
		path = custom
		dir = filepath.Dir(path)
	}
	if os.Getenv("OPENCODE_CONFIG") == "" {
		if _, err := os.Stat(filepath.Join(dir, "opencode.jsonc")); err == nil {
			return fmt.Errorf("existing OpenCode JSONC configuration requires explicit desktop MCP registration")
		}
	}
	config := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not read OpenCode configuration")
	}
	if err == nil && (json.Unmarshal(data, &config) != nil || config == nil) {
		return fmt.Errorf("invalid OpenCode JSON configuration; preserved unchanged")
	}
	entries := map[string]json.RawMessage{}
	if raw, ok := config["mcp"]; ok {
		if json.Unmarshal(raw, &entries) != nil || entries == nil {
			return fmt.Errorf("invalid OpenCode MCP configuration; preserved unchanged")
		}
	}
	if existing, exists := entries["vmbox-desktop"]; exists {
		var registered struct {
			Type    string   `json:"type"`
			Command []string `json:"command"`
		}
		if json.Unmarshal(existing, &registered) != nil || registered.Type != "local" || len(registered.Command) != 2 || registered.Command[0] != "vmbox-runtime" || registered.Command[1] != "desktop-mcp" {
			return nil
		}
	}
	entries["vmbox-desktop"], _ = json.Marshal(map[string]any{"type": "local", "command": []string{desktopRuntimePath(), "desktop-mcp"}})
	config["mcp"], _ = json.Marshal(entries)
	// --auto is a launch flag, so a terminal `opencode` would still prompt. The
	// config equivalent gives it the same permissions as a chat-started one.
	if _, set := config["permission"]; !set {
		config["permission"], _ = json.Marshal("allow")
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".desktop-mcp-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(append(encoded, '\n')); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp.Name(), path)
}
