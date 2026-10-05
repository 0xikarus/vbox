package boxruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type installDiskReader func(string) (int64, int64, error)

func readInstallDisk(path string) (int64, int64, error) {
	var stats syscall.Statfs_t
	for {
		err := syscall.Statfs(path, &stats)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) || path == filepath.Dir(path) {
			return 0, 0, err
		}
		path = filepath.Dir(path)
	}
	return int64(stats.Blocks) * stats.Bsize, int64(stats.Bavail) * stats.Bsize, nil
}

// Package managers may write both to the persistent home and the container
// layer, so check both filesystems immediately before managed installations.
func requireInstallDiskSpace(home string) error {
	return checkInstallDiskSpace(home, readInstallDisk)
}

func checkInstallDiskSpace(home string, read installDiskReader) error {
	paths := []string{home, "/"}
	for i, path := range paths {
		if i > 0 && path == home {
			continue
		}
		total, free, err := read(path)
		if err != nil {
			return fmt.Errorf("check disk space for tool installation on %s: %w", path, err)
		}
		if total <= 0 || free < 0 || free > total {
			return fmt.Errorf("disk free space unavailable for tool installation on %s", path)
		}
		minimum := total / 20
		if total%20 != 0 {
			minimum++
		}
		if free < minimum {
			return fmt.Errorf("insufficient disk space for tool installation on %s: %d bytes free of %d; at least 5%% free required", path, free, total)
		}
	}
	return nil
}
