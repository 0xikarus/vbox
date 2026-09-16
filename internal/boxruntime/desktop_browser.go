package boxruntime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RunDesktopBrowser uses the volume-backed home, not a temporary automation
// profile. Chromium's own profile lock reuses its existing browser instance.
func RunDesktopBrowser(ctx context.Context) error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("browser must run as the workspace user")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	profile := filepath.Join(home, ".config", "vmbox", "chromium")
	if err = os.MkdirAll(profile, 0700); err != nil {
		return err
	}
	args := []string{"--user-data-dir=" + profile, "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", "--no-first-run", "--no-default-browser-check"}
	// Dedicated worker containers can use their container boundary when their
	// host disallows Chromium's nested user/PID namespaces. An explicit false
	// keeps Chromium's sandbox enabled for hosts that support it.
	if chromiumNoSandbox(os.Getenv("VMBOX_CHROMIUM_NO_SANDBOX"), containerBoundaryPresent()) {
		args = append(args, "--no-sandbox")
	}
	cmd := exec.CommandContext(ctx, "chromium", args...)
	cmd.Env = append(os.Environ(), "DISPLAY=:99")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func chromiumNoSandbox(setting string, container bool) bool {
	switch strings.ToLower(strings.TrimSpace(setting)) {
	case "true":
		return true
	case "false":
		return false
	default:
		return container
	}
}

func containerBoundaryPresent() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return false
}
