package sharedworker

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func HostResources() (provider.HostResources, error) {
	data, err := os.ReadFile("/proc/meminfo")
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
	return provider.HostResources{MemoryTotalBytes: values["MemTotal"], MemoryAvailableBytes: values["MemAvailable"], SwapTotalBytes: values["SwapTotal"], SwapFreeBytes: values["SwapFree"], ObservedAt: time.Now().UTC()}, nil
}
