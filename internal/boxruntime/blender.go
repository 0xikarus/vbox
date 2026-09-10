package boxruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const blenderMCPVersion = "1.9.1"

func configureBlender(ctx context.Context, home string, progress io.Writer) error {
	path, err := toolSetupPath(home)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// Separate from custom Bash so configuring either never overwrites the other.
	if err = os.WriteFile(filepath.Join(filepath.Dir(path), "blender-enabled"), []byte("1\n"), 0600); err != nil {
		return err
	}
	return restoreBlender(ctx, home, progress)
}

func restoreBlender(ctx context.Context, home string, progress io.Writer) error {
	path, err := toolSetupPath(home)
	if err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(path), "blender-enabled")); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	fmt.Fprintln(progress, "Preparing Blender, desktop and Blender MCP (timeout 8 minutes)…")
	if err = installDesktopPackages(ctx, progress, true); err != nil {
		return fmt.Errorf("Blender/desktop installation failed: %w", err)
	}
	if err = installBlenderMCP(ctx, home, progress); err != nil {
		return fmt.Errorf("Blender MCP installation failed: %w", err)
	}
	fmt.Fprintf(progress, "Blender ready; desktop enabled; Blender MCP %s installed and registered for Codex and Claude. Launch Blender and open a new agent session.\n", blenderMCPVersion)
	return nil
}

func installBlenderMCP(ctx context.Context, home string, progress io.Writer) error {
	binDir := filepath.Join(home, ".local", "bin")
	server := filepath.Join(binDir, "blender-mcp")
	marker := filepath.Join(home, ".config", "vmbox", "blender-mcp-version")
	version, _ := os.ReadFile(marker)
	_, serverErr := os.Stat(server)
	if strings.TrimSpace(string(version)) != blenderMCPVersion || serverErr != nil {
		if err := os.MkdirAll(binDir, 0700); err != nil {
			return err
		}
		fmt.Fprintf(progress, "Installing pinned third-party Blender MCP %s…\n", blenderMCPVersion)
		cmd := exec.CommandContext(ctx, "pipx", "install", "--force", "blender-mcp=="+blenderMCPVersion)
		cmd.Env = append(os.Environ(), "HOME="+home, "PIPX_HOME="+filepath.Join(home, ".local", "share", "pipx"), "PIPX_BIN_DIR="+binDir, "PIP_DISABLE_PIP_VERSION_CHECK=1")
		cmd.Stdout, cmd.Stderr = progress, progress
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("install pinned server package: %w", err)
		}
		if _, err := os.Stat(server); err != nil {
			return fmt.Errorf("installed server executable unavailable: %w", err)
		}
		if err := os.WriteFile(marker, []byte(blenderMCPVersion+"\n"), 0600); err != nil {
			return err
		}
	}

	versionOutput, err := exec.CommandContext(ctx, "blender", "--version").Output()
	if err != nil {
		return fmt.Errorf("detect Blender version: %w", err)
	}
	match := regexp.MustCompile(`(?m)^Blender (\d+\.\d+)`).FindSubmatch(versionOutput)
	if len(match) != 2 {
		return fmt.Errorf("could not determine Blender add-on directory")
	}
	addonDir := filepath.Join(home, ".config", "blender", string(match[1]), "scripts", "addons")
	cmd := exec.CommandContext(ctx, server, "install-addon", "--addons-dir", addonDir)
	cmd.Env = append(os.Environ(), "HOME="+home, "DISABLE_TELEMETRY=true")
	cmd.Stdout, cmd.Stderr = progress, progress
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install Blender add-on: %w", err)
	}
	cmd = exec.CommandContext(ctx, "blender", "--background", "--python-expr", "import bpy; bpy.ops.preferences.addon_enable(module='blender_mcp'); bpy.ops.wm.save_userpref()")
	cmd.Env = append(os.Environ(), "HOME="+home)
	cmd.Stdout, cmd.Stderr = progress, progress
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("enable Blender add-on: %w", err)
	}

	env := []string{"BLENDER_HOST=127.0.0.1", "BLENDER_PORT=9876", "BLENDER_MCP_SAFE_MODE=1", "DISABLE_TELEMETRY=true"}
	if err := registerBlenderMCP(ctx, home, "codex", append([]string{"mcp", "add", "blender", "--env", env[0], "--env", env[1], "--env", env[2], "--env", env[3], "--"}, server), progress); err != nil {
		return err
	}
	return registerBlenderMCP(ctx, home, "claude", append([]string{"mcp", "add", "blender", "--scope", "user", "--env", env[0], "--env", env[1], "--env", env[2], "--env", env[3], "--"}, server), progress)
}

func registerBlenderMCP(ctx context.Context, home, agent string, addArgs []string, progress io.Writer) error {
	path, err := exec.LookPath(agent)
	if err != nil {
		fmt.Fprintf(progress, "%s is unavailable; skipping its Blender MCP registration.\n", agent)
		return nil
	}
	env := append(os.Environ(), "HOME="+home)
	probe := exec.CommandContext(ctx, path, "mcp", "get", "blender")
	probe.Env = env
	probeOutput, probeErr := probe.CombinedOutput()
	if probeErr == nil {
		fmt.Fprintf(progress, "Preserving existing %s Blender MCP configuration.\n", agent)
		return nil
	}
	if !strings.Contains(strings.ToLower(string(probeOutput)), "no mcp server named") {
		return fmt.Errorf("inspect existing Blender MCP configuration for %s: %w", agent, probeErr)
	}
	cmd := exec.CommandContext(ctx, path, addArgs...)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, progress, progress
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("register Blender MCP with %s: %w", agent, err)
	}
	return nil
}
