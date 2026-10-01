package sharedworker

import (
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

const (
	maxWorkerSlots = 32
	gib            = int64(1) << 30
	cpuStep        = 0.5
	memoryStepMiB  = 1024
)

// legacyBoxDefaults are the limits boxes received before settings existed.
var legacyBoxDefaults = provider.BoxLimits{CPU: 1, MemoryMiB: 2048, SwapMiB: 1024}

func seedSettings(capacity, retained int) *provider.WorkerSettings {
	slots := max(capacity, retained, 1)
	return &provider.WorkerSettings{Revision: 1, Slots: slots, BoxDefaults: legacyBoxDefaults}
}

// MachineSpecs reports the host the worker's boxes run on. Box containers are
// siblings of the supervisor, not children of its cgroup, so host totals are
// the right ceiling rather than the supervisor's own cgroup limit.
func MachineSpecs(root string) (provider.WorkerSpecs, error) {
	return machineSpecsFromPaths("/proc/meminfo", "/sys/fs/cgroup/cpu.max", root)
}

func machineSpecsFromPaths(meminfoPath, cpuMaxPath, root string) (provider.WorkerSpecs, error) {
	memory, err := hostMemory(meminfoPath)
	if err != nil {
		return provider.WorkerSpecs{}, err
	}
	specs := provider.WorkerSpecs{CPUs: float64(runtime.NumCPU()), MemoryBytes: memory.MemoryTotalBytes, SwapBytes: memory.SwapTotalBytes, Scope: "host"}
	if quota, ok := cpuQuota(cpuMaxPath); ok && quota < specs.CPUs {
		specs.CPUs = quota
	}
	var disk syscall.Statfs_t
	if err := syscall.Statfs(root, &disk); err != nil {
		return provider.WorkerSpecs{}, fmt.Errorf("read worker disk size: %w", err)
	}
	specs.DiskTotalBytes = int64(disk.Blocks) * int64(disk.Bsize)
	specs.DiskFreeBytes = int64(disk.Bavail) * int64(disk.Bsize)
	return specs, nil
}

func cpuQuota(path string) (float64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || fields[0] == "max" {
		return 0, false
	}
	quota, qerr := strconv.ParseFloat(fields[0], 64)
	period, perr := strconv.ParseFloat(fields[1], 64)
	if qerr != nil || perr != nil || quota <= 0 || period <= 0 {
		return 0, false
	}
	return quota / period, true
}

func (s *Store) perBoxLimits() bool {
	linux, ok := s.Runtime.(*LinuxRuntime)
	return ok && linux.Container != nil
}

func (s *Store) occupied() int {
	occupied := 0
	for _, slot := range s.state.Slots {
		if slot.WorkspaceID != "" {
			occupied++
		}
	}
	return occupied
}

// limits derives the enforced bounds from the machine. Callers hold s.mu.
func (s *Store) limits() (provider.WorkerLimits, error) {
	specs, err := s.Specs()
	if err != nil {
		return provider.WorkerLimits{}, fmt.Errorf("read machine specs: %w", err)
	}
	return limitsFor(specs, len(s.state.Slots), s.perBoxLimits()), nil
}

func limitsFor(specs provider.WorkerSpecs, slotsInUse int, perBox bool) provider.WorkerLimits {
	memoryGiB := max(specs.MemoryBytes/gib, 1)
	limits := provider.WorkerLimits{
		MinSlots: max(slotsInUse, 1),
		// Each box needs at least 1 GiB, so more slots than GiB of RAM cannot
		// all start. Overcommitting box memory beyond that is allowed.
		MaxSlots:      int(min(int64(maxWorkerSlots), memoryGiB)),
		PerBoxLimits:  perBox,
		BoxMin:        provider.BoxLimits{CPU: cpuStep, MemoryMiB: memoryStepMiB, SwapMiB: 0},
		BoxMax:        provider.BoxLimits{CPU: max(math.Floor(specs.CPUs/cpuStep)*cpuStep, cpuStep), MemoryMiB: memoryGiB * memoryStepMiB, SwapMiB: specs.SwapBytes / gib * memoryStepMiB},
		CPUStep:       cpuStep,
		MemoryStepMiB: memoryStepMiB,
	}
	// Retained slots are never evicted, even if the machine shrank.
	limits.MaxSlots = max(limits.MaxSlots, limits.MinSlots)
	return limits
}

func checkBoxLimits(box provider.BoxLimits, limits provider.WorkerLimits) error {
	steps := box.CPU / limits.CPUStep
	if math.IsNaN(box.CPU) || steps != math.Trunc(steps) || box.CPU < limits.BoxMin.CPU || box.CPU > limits.BoxMax.CPU {
		return fmt.Errorf("box CPU must be %g–%g in steps of %g", limits.BoxMin.CPU, limits.BoxMax.CPU, limits.CPUStep)
	}
	if box.MemoryMiB%limits.MemoryStepMiB != 0 || box.MemoryMiB < limits.BoxMin.MemoryMiB || box.MemoryMiB > limits.BoxMax.MemoryMiB {
		return fmt.Errorf("box memory must be %d–%d GiB", limits.BoxMin.MemoryMiB/1024, limits.BoxMax.MemoryMiB/1024)
	}
	if box.SwapMiB%limits.MemoryStepMiB != 0 || box.SwapMiB < limits.BoxMin.SwapMiB || box.SwapMiB > limits.BoxMax.SwapMiB {
		if limits.BoxMax.SwapMiB == 0 {
			return errors.New("this machine has no swap; box swap must be 0 GiB")
		}
		return fmt.Errorf("box swap must be 0–%d GiB", limits.BoxMax.SwapMiB/1024)
	}
	return nil
}

// clampBoxLimits fits saved defaults to a machine that may have shrunk since.
func clampBoxLimits(box provider.BoxLimits, limits provider.WorkerLimits) provider.BoxLimits {
	box.CPU = min(max(box.CPU, limits.BoxMin.CPU), limits.BoxMax.CPU)
	box.MemoryMiB = min(max(box.MemoryMiB, limits.BoxMin.MemoryMiB), limits.BoxMax.MemoryMiB)
	box.SwapMiB = min(max(box.SwapMiB, limits.BoxMin.SwapMiB), limits.BoxMax.SwapMiB)
	return box
}

// newBoxLimits resolves a new workspace's limits: the request when it names
// memory, otherwise the worker's defaults. Callers hold s.mu.
func (s *Store) newBoxLimits(resources provider.Resources) (provider.BoxLimits, error) {
	limits, err := s.limits()
	if err != nil {
		return provider.BoxLimits{}, err
	}
	box := clampBoxLimits(s.state.Settings.BoxDefaults, limits)
	if resources.MemoryMiB == 0 && resources.SwapMiB == 0 {
		return box, nil
	}
	box.MemoryMiB, box.SwapMiB = resources.MemoryMiB, resources.SwapMiB
	return box, checkBoxLimits(box, limits)
}

func (s *Store) workerConfig() (provider.WorkerConfig, error) {
	specs, err := s.Specs()
	if err != nil {
		return provider.WorkerConfig{}, fmt.Errorf("read machine specs: %w", err)
	}
	specs.IsolationTier = string(s.isolationStatus().Tier)
	limits := limitsFor(specs, len(s.state.Slots), s.perBoxLimits())
	settings := *s.state.Settings
	config := provider.WorkerConfig{Settings: settings, Specs: specs, Limits: limits, SlotsInUse: len(s.state.Slots), OccupiedSlots: s.occupied()}
	if limits.PerBoxLimits {
		config.Settings.BoxDefaults = clampBoxLimits(settings.BoxDefaults, limits)
		config.Overcommitted = int64(settings.Slots)*config.Settings.BoxDefaults.MemoryMiB<<20 > specs.MemoryBytes
	}
	return config, nil
}

// SlotCapacity is the configured number of logical slots.
func (s *Store) SlotCapacity() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Settings.Slots
}

// WorkerConfig reports the current settings, machine specs and bounds.
func (s *Store) WorkerConfig() (provider.WorkerConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return provider.WorkerConfig{}, s.failure
	}
	return s.workerConfig()
}

// SetSettings validates against the machine and applies immediately. Lowering
// slots below the logical slots that exist is refused, never evicted, and new
// defaults apply only to boxes created afterwards.
func (s *Store) SetSettings(next provider.WorkerSettings) (provider.WorkerConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return provider.WorkerConfig{}, s.failure
	}
	current := *s.state.Settings
	if next.Revision != current.Revision {
		return provider.WorkerConfig{}, errors.New("worker settings revision conflict; reload and retry")
	}
	config, err := s.workerConfig()
	if err != nil {
		return config, err
	}
	limits := config.Limits
	if next.Slots < limits.MinSlots {
		return config, fmt.Errorf("worker has %d slots in use; remove free slots or boxes before lowering below %d", config.SlotsInUse, limits.MinSlots)
	}
	if next.Slots > limits.MaxSlots {
		return config, fmt.Errorf("this machine supports at most %d slots (%d GiB RAM, 1 GiB minimum per box)", limits.MaxSlots, config.Specs.MemoryBytes/gib)
	}
	if limits.PerBoxLimits {
		if err := checkBoxLimits(next.BoxDefaults, limits); err != nil {
			return config, fmt.Errorf("default %w", err)
		}
	} else {
		next.BoxDefaults = current.BoxDefaults
	}
	next.Revision = current.Revision + 1
	s.state.Settings = &next
	if err := s.save(); err != nil {
		s.state.Settings = &current
		return config, err
	}
	return s.workerConfig()
}
