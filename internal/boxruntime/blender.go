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
	"strconv"
	"strings"
	"time"
)

const blenderMCPVersion = "1.9.1"

var installBlenderRelease = installPinnedBlender

func configureBlender(ctx context.Context, home string, progress io.Writer) error {
	if WorkspaceRoot() != "/data" && (os.Getuid() < 30000 || os.Getuid() >= 59000) {
		return errors.New("shared Blender requires a valid workspace UID")
	}
	path, err := toolSetupPath(home)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// Separate from custom Bash so configuring either never overwrites the other.
	marker := filepath.Join(filepath.Dir(path), "blender-enabled")
	if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
		if err = os.WriteFile(marker, []byte(blenderVersion+"\n"), 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return restoreBlender(ctx, home, progress)
}

func restoreBlender(ctx context.Context, home string, progress io.Writer) error {
	path, err := toolSetupPath(home)
	if err != nil {
		return err
	}
	configured, err := os.ReadFile(filepath.Join(filepath.Dir(path), "blender-enabled"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := requireInstallDiskSpace(home); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	blender := "blender"
	if strings.TrimSpace(string(configured)) == blenderVersion {
		if err := installDesktopPackages(ctx, progress, false); err != nil {
			return err
		}
		if err := installBlenderRelease(ctx, home, progress); err != nil {
			return err
		}
		blender = filepath.Join(home, "bin", "blender")
	} else if strings.TrimSpace(string(configured)) != "1" {
		return fmt.Errorf("unsupported retained Blender version; configuration preserved")
	}
	fmt.Fprintln(progress, "Preparing Blender, desktop and Blender MCP (timeout 8 minutes)…")
	if strings.TrimSpace(string(configured)) == "1" {
		if err = installDesktopPackages(ctx, progress, true); err != nil {
			return fmt.Errorf("Blender/desktop installation failed: %w", err)
		}
	}
	if err = installBlenderMCPBinary(ctx, home, blender, progress); err != nil {
		return fmt.Errorf("Blender MCP installation failed: %w", err)
	}
	fmt.Fprintf(progress, "Blender ready; desktop enabled; Blender MCP %s configured. Launch Blender and start its MCP server.\n", blenderMCPVersion)
	return nil
}

func installBlenderMCP(ctx context.Context, home string, progress io.Writer) error {
	return installBlenderMCPBinary(ctx, home, "blender", progress)
}

func installBlenderMCPBinary(ctx context.Context, home, blender string, progress io.Writer) error {
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
		imageServer := "/opt/vmbox/blender-mcp-" + blenderMCPVersion + "/bin/blender-mcp"
		if _, err := os.Stat(imageServer); err == nil && os.IsNotExist(serverErr) {
			if err := os.Symlink(imageServer, server); err != nil {
				return err
			}
		} else {
			cmd := exec.CommandContext(ctx, "pipx", "install", "--force", "blender-mcp=="+blenderMCPVersion)
			cmd.Env = append(os.Environ(), "HOME="+home, "PIPX_HOME="+filepath.Join(home, ".local", "share", "pipx"), "PIPX_BIN_DIR="+binDir, "PIP_DISABLE_PIP_VERSION_CHECK=1")
			cmd.Stdout, cmd.Stderr = progress, progress
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("install pinned server package: %w", err)
			}
		}
		if _, err := os.Stat(server); err != nil {
			return fmt.Errorf("installed server executable unavailable: %w", err)
		}
		if err := os.WriteFile(marker, []byte(blenderMCPVersion+"\n"), 0600); err != nil {
			return err
		}
	}

	versionOutput, err := exec.CommandContext(ctx, blender, "--version").Output()
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
	if WorkspaceRoot() != "/data" {
		if err := configureSharedBlenderAddon(filepath.Join(addonDir, "blender_mcp.py")); err != nil {
			return err
		}
	}
	cmd = exec.CommandContext(ctx, blender, "--background", "--python-expr", "import bpy; bpy.ops.preferences.addon_enable(module='blender_mcp'); bpy.ops.wm.save_userpref()")
	cmd.Env = append(os.Environ(), "HOME="+home)
	cmd.Stdout, cmd.Stderr = progress, progress
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("enable Blender add-on: %w", err)
	}

	env := []string{"BLENDER_HOST=127.0.0.1", "BLENDER_PORT=" + blenderMCPPort(), "BLENDER_MCP_SAFE_MODE=1", "DISABLE_TELEMETRY=true"}
	if err := registerBlenderMCP(ctx, home, "codex", append([]string{"mcp", "add", "blender", "--env", env[0], "--env", env[1], "--env", env[2], "--env", env[3], "--"}, server), progress); err != nil {
		return err
	}
	if err := registerBlenderMCP(ctx, home, "claude", append([]string{"mcp", "add", "blender", "--scope", "user", "--env", env[0], "--env", env[1], "--env", env[2], "--env", env[3], "--"}, server), progress); err != nil {
		return err
	}
	if blender != "blender" {
		return registerOpenCodeBlender(home, server)
	}
	return nil
}

func blenderMCPPort() string {
	if WorkspaceRoot() != "/data" {
		return strconv.Itoa(os.Getuid())
	}
	return "9876"
}

func configureSharedBlenderAddon(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	source := string(data)
	for _, replacement := range [][2]string{
		{"        self.host = host\n        self.port = port", "        self.host = '127.0.0.1'\n        self.port = os.getuid()"},
		{"        default=9876,", "        default=os.getuid(),\n        get=lambda self: os.getuid(),\n        set=lambda self, value: None,"},
	} {
		if strings.Count(source, replacement[0]) != 1 {
			return fmt.Errorf("unexpected Blender MCP add-on; cannot configure workspace port")
		}
		source = strings.Replace(source, replacement[0], replacement[1], 1)
	}
	return os.WriteFile(path, []byte(source), 0600)
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
