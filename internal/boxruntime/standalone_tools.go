package boxruntime

import (
	"context"
	"fmt"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Package installation runs once as container root before dropping privileges.
// The agent never receives sudo rights or a Docker socket.
func PrepareStandaloneToolPackages(ctx context.Context, tools []string, progress io.Writer) error {
	if err := v1.ValidateTools(tools); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	packages := []string{}
	if slices.Contains(tools, "blender") {
		for _, p := range []string{"blender", "pipx", "python3-venv", "python3-requests"} {
			c := exec.CommandContext(ctx, "dpkg-query", "-W", "-f=${Status}", p)
			out, err := c.Output()
			if err != nil || string(out) != "install ok installed" {
				packages = append(packages, p)
			}
		}
	}

	if slices.Contains(tools, "tor-socks5") {
		if _, err := exec.LookPath("tor"); err != nil {
			packages = append(packages, "tor")
		}
	}
	if len(packages) > 0 {
		for _, args := range [][]string{{"-o", "DPkg::Lock::Timeout=30", "update", "-qq"}, append([]string{"-o", "DPkg::Lock::Timeout=30", "install", "--no-remove", "-y", "--no-install-recommends"}, packages...)} {
			c := exec.CommandContext(ctx, "apt-get", args...)
			c.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
			c.Stdout = progress
			c.Stderr = progress
			if err := c.Run(); err != nil {
				return fmt.Errorf("prepare standalone packages: %w", err)
			}
		}
	}
	if slices.Contains(tools, "blender") {
		version, err := exec.CommandContext(ctx, "blender", "--version").Output()
		if err != nil || !strings.HasPrefix(string(version), "Blender 5.") {
			return fmt.Errorf("standalone Blender requires 5.x; rebuild the standalone image with ./install.sh --standalone")
		}
	}
	if slices.Contains(tools, "cloudflare-warp") {
		if !warpInstalled() {
			if err := installWARPPackage(ctx, progress); err != nil {
				return err
			}
		}
		if exec.CommandContext(ctx, "warp-cli", "--accept-tos", "status").Run() != nil {
			log, err := os.OpenFile("/tmp/vbox-warp-service.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			defer log.Close()
			c := exec.Command("warp-svc")
			c.Stdout = log
			c.Stderr = log
			if err = c.Start(); err != nil {
				return err
			}
			_ = c.Process.Release()
			ready := false
			for n := 0; n < 60; n++ {
				if exec.CommandContext(ctx, "warp-cli", "--accept-tos", "status").Run() == nil {
					ready = true
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(500 * time.Millisecond):
				}
			}
			if !ready {
				return fmt.Errorf("WARP service did not become ready")
			}

		}
	}
	return nil
}
func InstallStandaloneTools(ctx context.Context, home string, tools []string, progress io.Writer) error {
	if slices.Contains(tools, "blender") {
		if err := repairStandaloneBlenderBridge(ctx, home, progress); err != nil {
			return err
		}
		marker := filepath.Join(home, ".config/vmbox/blender-enabled")
		if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
			return err
		}
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			if err = os.WriteFile(marker, []byte("1\n"), 0600); err != nil {
				return err
			}
		}
	}
	if err := InstallTools(ctx, home, tools, progress); err != nil {
		return err
	}
	if slices.Contains(tools, "blender") {
		if err := registerOpenCodeBlender(home, filepath.Join(home, ".local/bin/blender-mcp")); err != nil {
			return err
		}
		version, err := exec.CommandContext(ctx, "blender", "--version").Output()
		if err != nil {
			return fmt.Errorf("cannot determine installed Blender version for instructions")
		}
		match := regexp.MustCompile(`(?m)^Blender (5\.[0-9]+\.[0-9]+)`).FindSubmatch(version)
		if len(match) != 2 {
			return fmt.Errorf("installed Blender is not a recognized 5.x release")
		}
		return EnsureStandaloneBlenderInstructions(home, string(match[1]))
	}
	return nil
}

// Persisted pipx environments can refer to an interpreter from the previous
// image (for example Python 3.11 -> 3.14). Recreate only our managed bridge when
// it can no longer start; retain agent configuration and all workspace files.
func repairStandaloneBlenderBridge(ctx context.Context, home string, progress io.Writer) error {
	server := filepath.Join(home, ".local/bin/blender-mcp")
	if _, err := os.Lstat(server); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	probe, cancel := context.WithTimeout(ctx, 15*time.Second)
	err := exec.CommandContext(probe, server, "--help").Run()
	cancel()
	if err == nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, "pipx", "reinstall", "--python", "python3", "blender-mcp")
	cmd.Env = append(os.Environ(), "HOME="+home, "PIPX_HOME="+filepath.Join(home, ".local/share/pipx"), "PIPX_BIN_DIR="+filepath.Join(home, ".local/bin"), "PIP_DISABLE_PIP_VERSION_CHECK=1")
	cmd.Stdout, cmd.Stderr = progress, progress
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("repair retained Blender MCP Python environment: %w", err)
	}
	return nil
}
