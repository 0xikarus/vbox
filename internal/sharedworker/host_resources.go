package sharedworker

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func HostResources(workspaceRoot string) (provider.HostResources, error) {
	result, err := hostResourcesFromPaths("/proc/meminfo", "/sys/fs/cgroup")
	if err != nil {
		return result, err
	}
	if _, err := os.Stat(workspaceRoot); errors.Is(err, os.ErrNotExist) {
		workspaceRoot = filepath.Dir(workspaceRoot)
	} else if err != nil {
		return result, err
	}
	metrics, err := readHostMetrics("/proc/loadavg", "/sys/fs/cgroup", workspaceRoot, nil, 0)
	if err != nil {
		return result, err
	}
	result.DiskTotalBytes, result.DiskUsedBytes = metrics.diskTotal, metrics.diskUsed
	result.CPUCores, result.CPULoad1, result.CPUPercent, result.CPUScope = metrics.cores, metrics.load1, metrics.percent, metrics.scope
	return result, nil
}

func hostResourcesFromPaths(meminfoPath, cgroupRoot string) (provider.HostResources, error) {
	data, err := os.ReadFile(meminfoPath)
	if err != nil {
		return provider.HostResources{}, err
	}
	values := map[string]int64{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) != 3 || parts[2] != "kB" {
			continue
		}
		key := strings.TrimSuffix(parts[0], ":")
		switch key {
		case "MemTotal", "MemAvailable", "SwapTotal", "SwapFree":
			value, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil || value < 0 {
				return provider.HostResources{}, errors.New("invalid host memory counters")
			}
			values[key] = value * 1024
		}
	}
	if err := scanner.Err(); err != nil {
		return provider.HostResources{}, err
	}
	if values["MemTotal"] == 0 || values["MemAvailable"] > values["MemTotal"] || values["SwapFree"] > values["SwapTotal"] {
		return provider.HostResources{}, errors.New("host memory counters unavailable")
	}
	result := provider.HostResources{MemoryTotalBytes: values["MemTotal"], MemoryAvailableBytes: values["MemAvailable"], SwapTotalBytes: values["SwapTotal"], SwapFreeBytes: values["SwapFree"], Scope: "host", SwapLimitKnown: true, ObservedAt: time.Now().UTC()}
	limit, finite, err := cgroupLimit(filepath.Join(cgroupRoot, "memory.max"))
	if errors.Is(err, os.ErrNotExist) || !finite && err == nil {
		return result, nil
	}
	if err != nil {
		return provider.HostResources{}, fmt.Errorf("read worker memory limit: %w", err)
	}
	current, err := cgroupCounter(filepath.Join(cgroupRoot, "memory.current"))
	if err != nil {
		return provider.HostResources{}, fmt.Errorf("read worker memory usage: %w", err)
	}
	result.Scope = "cgroup"
	result.MemoryTotalBytes = limit
	result.MemoryAvailableBytes = max(0, limit-current)
	result.SwapTotalBytes = 0
	result.SwapFreeBytes = 0
	result.SwapLimitKnown = false
	swapLimit, swapFinite, err := cgroupLimit(filepath.Join(cgroupRoot, "memory.swap.max"))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return provider.HostResources{}, fmt.Errorf("read worker swap limit: %w", err)
	}
	result.SwapLimitKnown = true
	if !swapFinite {
		result.SwapUnlimited = true
		return result, nil
	}
	swapCurrent, err := cgroupCounter(filepath.Join(cgroupRoot, "memory.swap.current"))
	if err != nil {
		return provider.HostResources{}, fmt.Errorf("read worker swap usage: %w", err)
	}
	result.SwapTotalBytes = swapLimit
	result.SwapFreeBytes = max(0, swapLimit-swapCurrent)
	return result, nil
}

func cgroupLimit(path string) (int64, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false, err
	}
	value := strings.TrimSpace(string(data))
	if value == "max" {
		return 0, false, nil
	}
	limit, err := strconv.ParseInt(value, 10, 64)
	if err != nil || limit < 0 {
		return 0, false, errors.New("invalid cgroup resource limit")
	}
	return limit, true, nil
}

func cgroupCounter(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("invalid cgroup resource counter")
	}
	return value, nil
}
