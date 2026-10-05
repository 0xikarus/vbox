package boxruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	StaleTempAge               = 24 * time.Hour
	maxCleanupEntries          = 10000
	maxCleanupCandidates       = 128
	maxCleanupBytes      int64 = 2 << 30
)

type TempCleanupResult struct {
	Removed        int   `json:"removed"`
	ReclaimedBytes int64 `json:"reclaimedBytes"`
	Skipped        int   `json:"skipped"`
}

type cleanupEntry struct {
	path string
	dev  uint64
	ino  uint64
	size int64
}

type inodeKey struct{ dev, ino uint64 }

func inode(info os.FileInfo) (inodeKey, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return inodeKey{}, false
	}
	return inodeKey{uint64(st.Dev), st.Ino}, true
}

func ownedBy(info os.FileInfo, uid int) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(uid)
}

func allocatedBytes(info os.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}
	return 0
}

func chromiumTemp(name string) bool {
	return strings.HasPrefix(name, "chrome-") || strings.HasPrefix(name, "chromium-") ||
		strings.HasPrefix(name, "puppeteer-") || strings.HasPrefix(name, "puppeteer_dev_chrome_profile-") ||
		strings.HasPrefix(name, ".org.chromium.Chromium.") || strings.HasPrefix(name, "org.chromium.Chromium.")
}

func coreDump(name string) bool { return name == "core" || strings.HasPrefix(name, "core.") }

// collectOpenInodes fails closed if /proc cannot be inspected completely. A
// stale timestamp alone does not make a temp tree safe to remove.
func collectOpenInodes(ctx context.Context, uid int) (map[inodeKey]bool, error) {
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	open := make(map[inodeKey]bool)
	checked := 0
	for _, process := range processes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := strconv.Atoi(process.Name()); err != nil {
			continue
		}
		base := filepath.Join("/proc", process.Name())
		info, err := os.Stat(base)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !ownedBy(info, uid) {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(base, "fd"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		paths := make([]string, 0, len(fds)+1)
		paths = append(paths, filepath.Join(base, "cwd"))
		for _, fd := range fds {
			paths = append(paths, filepath.Join(base, "fd", fd.Name()))
		}
		for _, path := range paths {
			checked++
			if checked > maxCleanupEntries {
				return nil, errors.New("too many open files to verify cleanup")
			}
			info, err := os.Stat(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if key, ok := inode(info); ok {
				open[key] = true
			}
		}
	}
	return open, ctx.Err()
}

func collectStaleTree(ctx context.Context, path string, uid int, cutoff time.Time, open map[inodeKey]bool) ([]cleanupEntry, int64, error) {
	var entries []cleanupEntry
	var size int64
	err := filepath.Walk(path, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(entries) >= maxCleanupEntries || !ownedBy(info, uid) || !info.ModTime().Before(cutoff) {
			return errors.New("temp tree is large, active, or owned by another user")
		}
		key, ok := inode(info)
		if !ok || open[key] {
			return errors.New("temp tree contains an open file")
		}
		if !(info.Mode().IsRegular() || info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return errors.New("temp tree contains a special file")
		}
		entries = append(entries, cleanupEntry{name, key.dev, key.ino, allocatedBytes(info)})
		size += allocatedBytes(info)
		return nil
	})
	return entries, size, err
}

func removeStaleTree(entries []cleanupEntry, uid int, cutoff time.Time, open map[inodeKey]bool) (int, int64, error) {
	removed, bytes := 0, int64(0)
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		info, err := os.Lstat(entry.path)
		if err != nil {
			return removed, bytes, err
		}
		key, ok := inode(info)
		if !ok || key != (inodeKey{entry.dev, entry.ino}) || !ownedBy(info, uid) || (!info.IsDir() && !info.ModTime().Before(cutoff)) || open[key] {
			return removed, bytes, errors.New("temp tree changed during cleanup")
		}
		if err := os.Remove(entry.path); err != nil {
			return removed, bytes, err
		}
		removed++
		bytes += entry.size
	}
	return removed, bytes, nil
}

func safeCleanupRoot(path string, uid int) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && (ownedBy(info, uid) || info.Mode().Perm()&01000 != 0)
}

func cleanupTempAt(ctx context.Context, workspaceRoot, privateTemp, systemTemp string, uid int, now time.Time, open map[inodeKey]bool) (TempCleanupResult, error) {
	cutoff := now.Add(-StaleTempAge)
	result := TempCleanupResult{}
	candidates := 0
	for _, root := range []string{privateTemp, systemTemp} {
		if !safeCleanupRoot(root, uid) {
			continue
		}
		children, err := os.ReadDir(root)
		if err != nil {
			return result, err
		}
		for _, child := range children {
			if !chromiumTemp(child.Name()) || !child.IsDir() {
				continue
			}
			if candidates >= maxCleanupCandidates || result.ReclaimedBytes >= maxCleanupBytes {
				result.Skipped++
				continue
			}
			candidates++
			entries, size, err := collectStaleTree(ctx, filepath.Join(root, child.Name()), uid, cutoff, open)
			if err != nil || result.ReclaimedBytes+size > maxCleanupBytes {
				result.Skipped++
				continue
			}
			count, bytes, err := removeStaleTree(entries, uid, cutoff, open)
			result.Removed += count
			result.ReclaimedBytes += bytes
			if err != nil {
				result.Skipped++
			}
		}
	}
	// Search likely dump locations to depth four, with the same global work
	// budget. This also catches core dumps inside the persistent workspace.
	for _, root := range []string{privateTemp, systemTemp, filepath.Join(workspaceRoot, "workspace"), filepath.Join(workspaceRoot, "home")} {
		if !safeCleanupRoot(root, uid) {
			continue
		}
		seen := 0
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || ctx.Err() != nil || info == nil {
				result.Skipped++
				return filepath.SkipAll
			}
			seen++
			if seen > maxCleanupEntries {
				result.Skipped++
				return filepath.SkipAll
			}
			if info.IsDir() {
				if path != root && (!ownedBy(info, uid) || strings.Count(strings.TrimPrefix(path, root), string(os.PathSeparator)) > 4) {
					return filepath.SkipDir
				}
				return nil
			}
			if !coreDump(info.Name()) || !info.Mode().IsRegular() || !ownedBy(info, uid) || !info.ModTime().Before(cutoff) || result.ReclaimedBytes+allocatedBytes(info) > maxCleanupBytes {
				return nil
			}
			key, ok := inode(info)
			if !ok || open[key] {
				result.Skipped++
				return nil
			}
			fresh, err := os.Lstat(path)
			if err != nil || !fresh.Mode().IsRegular() || !ownedBy(fresh, uid) || !fresh.ModTime().Before(cutoff) {
				result.Skipped++
				return nil
			}
			freshKey, valid := inode(fresh)
			if !valid || freshKey != key || os.Remove(path) != nil {
				result.Skipped++
				return nil
			}
			result.Removed++
			result.ReclaimedBytes += allocatedBytes(info)
			return nil
		})
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func CleanupStaleTemp(ctx context.Context) (TempCleanupResult, error) {
	root := WorkspaceRoot()
	if !filepath.IsAbs(root) || root == "/" {
		return TempCleanupResult{}, fmt.Errorf("workspace root unavailable for temp cleanup")
	}
	open, err := collectOpenInodes(ctx, os.Geteuid())
	if err != nil {
		return TempCleanupResult{}, fmt.Errorf("inspect open files before cleanup: %w", err)
	}
	return cleanupTempAt(ctx, root, os.Getenv("TMPDIR"), "/tmp", os.Geteuid(), time.Now(), open)
}
