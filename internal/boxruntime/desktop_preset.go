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

func configureDesktop(ctx context.Context, home string, progress io.Writer) error {
	path, err := toolSetupPath(home)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "desktop-enabled"), []byte("1\n"), 0600); err != nil {
		return err
	}
	return restoreDesktop(ctx, home, progress)
}

func restoreDesktop(ctx context.Context, home string, progress io.Writer) error {
	path, err := toolSetupPath(home)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "desktop-enabled")); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	fmt.Fprintln(progress, "Preparing desktop and browser (timeout 5 minutes)…")
	return installDesktopPackages(ctx, progress, false)
}
