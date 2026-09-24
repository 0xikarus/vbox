package boxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// RunDesktopFolders owns one watcher per box even if Desktop is opened again.
// It runs in its own tmux window so a running VNC session need not be restarted
// when a newer workspace runtime is installed.
func RunDesktopFolders(ctx context.Context, assignment string) error {
	if _, err := NativeSessions(ctx, assignment); err != nil {
		return err
	}
	lockPath := filepath.Join(WorkloadHome(), ".config", "vmbox", "workspace-desktop-folders.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil
		}
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	watchWorkspaceDesktopFolders(ctx)
	return nil
}

// The desktop contains links to the workspace's top-level folders, rather
// than the workspace itself, so project directories appear without every file
// in the workspace cluttering the desktop.
func watchWorkspaceDesktopFolders(ctx context.Context) {
	home := os.Getenv("HOME")
	if home == "" {
		home = WorkloadHome()
	}
	desktopDir := filepath.Join(home, "Desktop")
	if output, err := exec.CommandContext(ctx, "xdg-user-dir", "DESKTOP").Output(); err == nil {
		if selected := strings.TrimSpace(string(output)); selected != "" && selected != home && filepath.IsAbs(selected) {
			desktopDir = selected
		}
	}
	workspaceDir := WorkspaceDirectory()
	if withinWorkspace(desktopDir, workspaceDir) {
		return
	}
	manifest := filepath.Join(home, ".config", "vmbox", "workspace-desktop-folders.json")
	sync := func() {
		if err := syncWorkspaceDesktopFolders(workspaceDir, desktopDir, manifest); err != nil {
			fmt.Fprintln(os.Stderr, "workspace desktop folders:", err)
		}
	}
	sync()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sync()
		}
	}
}

func withinWorkspace(path, workspace string) bool {
	rel, err := filepath.Rel(workspace, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))))
}

// Only links recorded in our private manifest are removed, and only while
// they still point to the exact workspace folder we created them for. Files,
// custom launchers, and owner-edited links are left untouched.
func syncWorkspaceDesktopFolders(workspaceDir, desktopDir, manifest string) error {
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(desktopDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(manifest), 0o700); err != nil {
		return err
	}
	var prior []string
	if data, err := os.ReadFile(manifest); err == nil {
		if json.Unmarshal(data, &prior) != nil {
			return fmt.Errorf("invalid workspace folder manifest")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	managed := make(map[string]bool, len(prior))
	for _, name := range prior {
		if name != "" && name != "." && name != ".." && filepath.Base(name) == name {
			managed[name] = true
		}
	}
	current := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		current[name] = true
		target := filepath.Join(desktopDir, name)
		source := filepath.Join(workspaceDir, name)
		_, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Symlink(source, target); err != nil {
				if errors.Is(err, os.ErrExist) {
					continue
				}
				return err
			}
			managed[name] = true
			continue
		}
		if err != nil {
			return err
		}
		link, err := os.Readlink(target)
		if err != nil || link != source {
			delete(managed, name)
		}
	}
	for name := range managed {
		if current[name] {
			continue
		}
		target := filepath.Join(desktopDir, name)
		link, err := os.Readlink(target)
		if err == nil && link == filepath.Join(workspaceDir, name) {
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		delete(managed, name)
	}
	updated := make([]string, 0, len(managed))
	for name := range managed {
		updated = append(updated, name)
	}
	slices.Sort(updated)
	slices.Sort(prior)
	if slices.Equal(prior, updated) {
		return nil
	}
	data, err := json.Marshal(updated)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(manifest), ".workspace-desktop-folders-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), manifest)
}
