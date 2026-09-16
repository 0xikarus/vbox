package boxruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func WorkspaceRoot() string {
	if root := os.Getenv("VMBOX_WORKSPACE_ROOT"); root != "" {
		return root
	}
	return "/data"
}

func WorkloadHome() string { return filepath.Join(WorkspaceRoot(), "home") }

func WorkspaceDirectory() string { return filepath.Join(WorkspaceRoot(), "workspace") }

func WorkspacePath(path string) string {
	if path == "/data" {
		return WorkspaceRoot()
	}
	if strings.HasPrefix(path, "/data/") {
		return WorkspaceRoot() + strings.TrimPrefix(path, "/data")
	}
	return path
}

func DesktopDisplay() string {
	if display := os.Getenv("VMBOX_DESKTOP_DISPLAY"); display != "" {
		return display
	}
	return ":99"
}

func ValidateWorkspaceEnvironment() error {
	root := WorkspaceRoot()
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.ContainsAny(root, "\x00\r\n") {
		return fmt.Errorf("invalid workspace root")
	}
	display := DesktopDisplay()
	number, err := strconv.Atoi(strings.TrimPrefix(display, ":"))
	if err != nil || display != ":"+strconv.Itoa(number) || number < 1 || number > 65535 {
		return fmt.Errorf("invalid desktop display")
	}
	if root != "/data" && os.Geteuid() == 0 {
		return fmt.Errorf("shared workspace runtime must run as its dedicated user")
	}
	return nil
}

func desktopRuntimePath() string { return filepath.Join(WorkloadHome(), "bin", "vmbox-runtime") }
