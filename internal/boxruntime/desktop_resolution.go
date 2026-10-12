package boxruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func desktopResolutionPath(home string) string {
	return filepath.Join(home, ".config", "vmbox", "desktop-resolution")
}

// ConfigureDesktopResolution retains the choice on the box volume, so a new
// worker starts the same screen after hibernation or replacement.
func ConfigureDesktopResolution(home, resolution string) error {
	if err := v1.ValidateDesktopResolution(resolution); err != nil {
		return err
	}
	if !filepath.IsAbs(home) || home == "/" {
		return fmt.Errorf("desktop resolution requires a persistent absolute home")
	}
	if resolution == "" {
		resolution = v1.DefaultDesktopResolution
	}
	path := desktopResolutionPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".desktop-resolution-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(resolution + "\n"); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func desktopDimensions() (int, int) {
	data, err := os.ReadFile(desktopResolutionPath(WorkloadHome()))
	if err == nil && strings.TrimSpace(string(data)) == "1280x720" {
		return 1280, 720
	}
	return defaultDesktopWidth, defaultDesktopHeight
}
