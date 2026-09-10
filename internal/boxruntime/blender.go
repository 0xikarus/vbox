package boxruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	fmt.Fprintln(progress, "Preparing Blender and desktop (distribution packages; timeout 5 minutes)…")
	if err = installDesktopPackages(ctx, progress, true); err != nil {
		return fmt.Errorf("Blender/desktop installation failed: %w", err)
	}
	fmt.Fprintln(progress, "Blender ready; desktop enabled. Open Desktop, then launch blender. MCP is not installed.")
	return nil
}
