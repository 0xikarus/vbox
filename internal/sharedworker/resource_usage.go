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
	s.mu.Lock()
	defer s.mu.Unlock()
	linux, ok := s.Runtime.(*LinuxRuntime)
	if !ok || linux.Container == nil {
		return provider.ResourceUsage{}, provider.ErrUnsupported
	}
	slot, exists := s.state.Slots[id]
	if !exists || slot.WorkspaceID == "" || slot.State != provider.StateRunning {
		return provider.ResourceUsage{}, provider.ErrNotFound
	}
	workspace, exists := s.state.Workspaces[slot.WorkspaceID]
	if !exists {
		return provider.ResourceUsage{}, provider.ErrNotFound
	}
	current, err := linux.Container.inspect(ctx, workspace)
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
	used, observed := s.cachedDiskUsage(workspace, linux.workspaceRoot(workspace))
	usage := provider.ResourceUsage{MemoryUsedBytes: memory, SwapUsedBytes: swap, DiskUsedBytes: used, DiskObservedAt: observed, ObservedAt: time.Now().UTC()}
	if workspace.SizeGiB > 0 {
		total := workspace.SizeGiB << 30
		usage.DiskTotalBytes = &total
	}
	if total, used, err := filesystemUsage(linux.workspaceRoot(workspace)); err == nil {
		usage.HostDiskTotalBytes, usage.HostDiskUsedBytes = &total, &used
	}
	return usage, nil
}

// Disk scanning is deliberately asynchronous. Chat polling always gets fresh
// cgroup counters while the expensive workspace walk is reused for five minutes.
func (s *Store) cachedDiskUsage(workspace Workspace, path string) (*int64, *time.Time) {
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
		go s.scanDisk(workspace.ID, path)
	}
	if entry.used == nil {
		return nil, nil
	}
	used := *entry.used
	observed := entry.observedAt
	return &used, &observed
}

func (s *Store) scanDisk(id, path string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	used, err := scanDiskBytes(ctx, path)
	s.diskMu.Lock()
	defer s.diskMu.Unlock()
	entry := s.diskCache[id]
	entry.running = false
	if err == nil {
		entry.used = &used
		entry.observedAt = time.Now().UTC()
	}
	s.diskCache[id] = entry
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
		return errors.New("workspace has too many files for a disk scan")
	}
	if readErr != nil {
		return readErr
	}
	return waitErr
}
