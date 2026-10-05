package sharedworker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

const diskObservationInterval = 5 * time.Minute

var errDiskTooManyFiles = errors.New("too many files to measure")

func slotMemoryUsage(cgroupRoot string) (int64, int64, error) {
	memory, err := cgroupCounter(filepath.Join(cgroupRoot, "memory.current"))
	if err != nil {
		return 0, 0, fmt.Errorf("read slot memory usage: %w", err)
	}
	swap, err := cgroupCounter(filepath.Join(cgroupRoot, "memory.swap.current"))
	if err != nil {
		return 0, 0, fmt.Errorf("read slot swap usage: %w", err)
	}
	return memory, swap, nil
}

func (s *Store) ResourceUsage(ctx context.Context, id string) (provider.ResourceUsage, error) {
	linux, ok := s.Runtime.(*LinuxRuntime)
	if !ok || linux.Container == nil {
		return provider.ResourceUsage{}, provider.ErrUnsupported
	}
	return s.resourceUsageWithInspect(ctx, id, linux.Container.inspect)
}

func (s *Store) resourceUsageWithInspect(ctx context.Context, id string, inspect func(context.Context, Workspace) (*containerInspect, error)) (provider.ResourceUsage, error) {
	linux := s.Runtime.(*LinuxRuntime)
	s.mu.Lock()
	slot, exists := s.state.Slots[id]
	if !exists || slot.WorkspaceID == "" || slot.State != provider.StateRunning {
		s.mu.Unlock()
		return provider.ResourceUsage{}, provider.ErrNotFound
	}
	workspace, exists := s.state.Workspaces[slot.WorkspaceID]
	if !exists {
		s.mu.Unlock()
		return provider.ResourceUsage{}, provider.ErrNotFound
	}
	workspaceRoot := linux.workspaceRoot(workspace)
	s.mu.Unlock()
	current, err := inspect(ctx, workspace)
	if err != nil {
		return provider.ResourceUsage{}, err
	}
	if current == nil || !current.State.Running || current.State.Pid <= 1 {
		return provider.ResourceUsage{}, errors.New("live container unavailable")
	}
	cgroup := filepath.Join("/proc", strconv.Itoa(current.State.Pid), "root/sys/fs/cgroup")
	memory, swap, err := slotMemoryUsage(cgroup)
	if err != nil {
		return provider.ResourceUsage{}, err
	}
	workspaceUsed, writableUsed, observed, diskReason := s.cachedDiskUsage(workspace, workspaceRoot, linux.Container.writableSize)
	usage := provider.ResourceUsage{MemoryUsedBytes: memory, SwapUsedBytes: swap, DiskWorkspaceBytes: workspaceUsed, DiskWritableBytes: writableUsed, DiskObservedAt: observed, DiskUnavailableReason: diskReason, ObservedAt: time.Now().UTC()}
	if workspaceUsed != nil {
		used := *workspaceUsed
		if writableUsed != nil {
			used += *writableUsed
		} else {
			usage.DiskPartial = true
		}
		usage.DiskUsedBytes = &used
	}
	if workspace.SizeGiB > 0 {
		total := workspace.SizeGiB << 30
		usage.DiskTotalBytes = &total
	}
	if total, used, free, err := filesystemUsage(workspaceRoot); err == nil {
		usage.HostDiskTotalBytes, usage.HostDiskUsedBytes, usage.HostDiskFreeBytes = &total, &used, &free
	}
	return usage, nil
}

// Disk scanning is deliberately asynchronous. Chat polling always gets fresh
// cgroup counters while the expensive workspace walk is reused for five minutes.
func (s *Store) cachedDiskUsage(workspace Workspace, path string, writableSize func(context.Context, Workspace) (int64, error)) (*int64, *int64, *time.Time, string) {
	s.diskMu.Lock()
	defer s.diskMu.Unlock()
	if s.diskCache == nil {
		s.diskCache = make(map[string]diskObservation)
	}
	entry := s.diskCache[workspace.ID]
	if !entry.running && time.Since(entry.startedAt) >= diskObservationInterval {
		entry.running = true
		entry.startedAt = time.Now()
		s.diskCache[workspace.ID] = entry
		go s.scanDisk(workspace, path, writableSize)
	}
	if entry.workspaceUsed == nil {
		return nil, nil, nil, entry.unavailableReason
	}
	workspaceUsed := *entry.workspaceUsed
	var writableUsed *int64
	if entry.writableUsed != nil {
		value := *entry.writableUsed
		writableUsed = &value
	}
	observed := entry.observedAt
	return &workspaceUsed, writableUsed, &observed, entry.unavailableReason
}

func (s *Store) scanDisk(workspace Workspace, path string, writableSize func(context.Context, Workspace) (int64, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	used, err := scanDiskBytes(ctx, path)
	var writable int64
	var writableErr error
	if err == nil && writableSize != nil {
		writable, writableErr = writableSize(ctx, workspace)
	}
	s.diskMu.Lock()
	defer s.diskMu.Unlock()
	entry := s.diskCache[workspace.ID]
	entry.running = false
	if err == nil {
		entry.workspaceUsed = &used
		entry.observedAt = time.Now().UTC()
		entry.writableUsed = nil
		entry.unavailableReason = "container writable layer size unavailable"
		if writableErr == nil && writableSize != nil {
			entry.writableUsed = &writable
			entry.unavailableReason = ""
		}
	} else if errors.Is(err, errDiskTooManyFiles) {
		entry.workspaceUsed = nil
		entry.writableUsed = nil
		entry.unavailableReason = errDiskTooManyFiles.Error()
	} else {
		entry.workspaceUsed = nil
		entry.writableUsed = nil
		entry.unavailableReason = "workspace disk scan unavailable"
	}
	s.diskCache[workspace.ID] = entry
}

func scanDiskBytes(ctx context.Context, path string) (int64, error) {
	if _, err := os.Stat(path); err != nil {
		return 0, err
	}
	if err := diskFileCount(ctx, path, 200000); err != nil {
		return 0, err
	}
	command := lowPriorityCommand(ctx, "du", "-sx", "-B1", path)
	out, err := command.Output()
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, errors.New("disk scan returned no bytes")
	}
	value, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("disk scan returned invalid bytes")
	}
	return value, nil
}

func lowPriorityCommand(ctx context.Context, argv ...string) *exec.Cmd {
	program, args := "nice", append([]string{"-n", "19"}, argv...)
	if _, err := exec.LookPath("ionice"); err == nil {
		program, args = "ionice", append([]string{"-c3", "nice"}, args...)
	}
	return exec.CommandContext(ctx, program, args...)
}

func diskFileCount(ctx context.Context, path string, limit int64) error {
	command := lowPriorityCommand(ctx, "find", path, "-xdev", "-printf", ".")
	output, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	count, readErr := io.Copy(io.Discard, io.LimitReader(output, limit+1))
	if count > limit || readErr != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if count > limit {
		return errDiskTooManyFiles
	}
	if readErr != nil {
		return readErr
	}
	return waitErr
}
