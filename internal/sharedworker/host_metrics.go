package sharedworker

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type hostMetrics struct {
	diskTotal, diskUsed   int64
	cores, load1, percent float64
	scope                 string
}

type cpuObservation struct {
	usage int64
	at    time.Time
}

var cpuObservations = struct {
	sync.Mutex
	values map[string]cpuObservation
}{values: make(map[string]cpuObservation)}

type statfsReader func(string) (int64, int64, error)

func filesystemUsage(path string) (int64, int64, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return 0, 0, err
	}
	total := int64(stats.Blocks) * stats.Bsize
	used := int64(stats.Blocks-stats.Bfree) * stats.Bsize
	if total <= 0 || used < 0 || used > total {
		return 0, 0, errors.New("invalid filesystem capacity")
	}
	return total, used, nil
}

func readHostMetrics(loadavgPath, cgroupRoot, diskPath string, statfs statfsReader, hostCores float64) (hostMetrics, error) {
	if statfs == nil {
		statfs = filesystemUsage
	}
	total, used, err := statfs(diskPath)
	if err != nil {
		return hostMetrics{}, fmt.Errorf("read workspace filesystem: %w", err)
	}
	loadData, err := os.ReadFile(loadavgPath)
	if err != nil {
		return hostMetrics{}, fmt.Errorf("read CPU load: %w", err)
	}
	parts := strings.Fields(string(loadData))
	if len(parts) < 1 {
		return hostMetrics{}, errors.New("CPU load unavailable")
	}
	load, err := strconv.ParseFloat(parts[0], 64)
	if err != nil || load < 0 {
		return hostMetrics{}, errors.New("invalid CPU load")
	}
	if hostCores <= 0 {
		hostCores = float64(runtime.NumCPU())
	}
	if hostCores <= 0 {
		hostCores = 1
	}
	cores, scope := hostCores, "host"
	if data, err := os.ReadFile(filepath.Join(cgroupRoot, "cpu.max")); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) == 2 && fields[0] != "max" {
			quota, e1 := strconv.ParseFloat(fields[0], 64)
			period, e2 := strconv.ParseFloat(fields[1], 64)
			if e1 == nil && e2 == nil && quota > 0 && period > 0 {
				cores, scope = quota/period, "cgroup"
			}
		}
	}
	percent := min(100, load/cores*100)
	if scope == "cgroup" {
		if usage, err := cpuUsageMicros(filepath.Join(cgroupRoot, "cpu.stat")); err == nil {
			now := time.Now()
			cpuObservations.Lock()
			previous, exists := cpuObservations.values[cgroupRoot]
			cpuObservations.values[cgroupRoot] = cpuObservation{usage, now}
			cpuObservations.Unlock()
			if exists && usage >= previous.usage && now.After(previous.at) {
				percent = cpuPercentFromDelta(usage-previous.usage, now.Sub(previous.at), cores)
			}
		}
	}
	return hostMetrics{diskTotal: total, diskUsed: used, cores: cores, load1: load, percent: percent, scope: scope}, nil
}

func cpuUsageMicros(path string) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scan := bufio.NewScanner(file)
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) == 2 && fields[0] == "usage_usec" {
			value, err := strconv.ParseInt(fields[1], 10, 64)
			if err == nil && value >= 0 {
				return value, nil
			}
			return 0, errors.New("invalid CPU usage")
		}
	}
	if err := scan.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("CPU usage unavailable")
}

func cpuPercentFromDelta(usageMicros int64, elapsed time.Duration, cores float64) float64 {
	if usageMicros < 0 || elapsed <= 0 || cores <= 0 {
		return 0
	}
	return min(100, float64(usageMicros)/float64(elapsed.Microseconds())/cores*100)
}
