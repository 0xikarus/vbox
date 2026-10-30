package standalone

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// A recreated container has a new hostname and PID namespace. Chromium's
// persistent singleton symlinks can therefore name an owner that no longer
// exists. Before starting the standalone workload, remove only these disposable
// locks, and preserve them whenever a live process uses the same profile.
func cleanupChromiumLocks(home, procRoot string) error {
	for _, profile := range []string{filepath.Join(home, ".config/vmbox/chromium"), filepath.Join(home, ".config/chromium")} {
		entries, err := os.ReadDir(procRoot)
		if err != nil {
			return err
		}
		live := false
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			cmd, err := os.ReadFile(filepath.Join(procRoot, entry.Name(), "cmdline"))
			if err != nil {
				continue
			}
			args := bytes.Split(cmd, []byte{0})
			for i, arg := range args {
				if string(arg) == "--user-data-dir="+profile || (string(arg) == "--user-data-dir" && i+1 < len(args) && string(args[i+1]) == profile) {
					live = true
					break
				}
			}
			if live {
				break
			}
		}
		if live {
			continue
		}
		for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
			path := filepath.Join(profile, name)
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("unexpected Chromium singleton file; preserved")
			}
			if err = os.Remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}
