package boxruntime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	// Dedicated worker containers can explicitly use their box boundary when
	// their host disallows Chromium's nested user/PID namespaces. Never silently
	// retry a failed sandbox launch with weaker settings.
	if os.Getenv("VMBOX_CHROMIUM_NO_SANDBOX") == "true" {
		args = append(args, "--no-sandbox")
	}
	cmd := exec.CommandContext(ctx, "chromium", args...)
	cmd.Env = append(os.Environ(), "DISPLAY=:99")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
