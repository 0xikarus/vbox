package sharedworker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestOldWorkerHostResourcesDoNotReportZeroFree(t *testing.T) {
	var old provider.HostResources
	if err := json.Unmarshal([]byte(`{"diskTotalBytes":100,"diskUsedBytes":90}`), &old); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if old.DiskFreeBytes != nil || strings.Contains(string(encoded), "diskFreeBytes") {
		t.Fatalf("old worker free-space field: %s", encoded)
	}
}

func TestHostResourcesUsesFiniteCgroupLimit(t *testing.T) {
	dir := t.TempDir()
	writeResourceFixture(t, dir, "meminfo", "MemTotal:       8388608 kB\nMemAvailable:   4194304 kB\nSwapTotal:      4194304 kB\nSwapFree:       2097152 kB\n")
	writeResourceFixture(t, dir, "memory.max", "4294967296\n")
	writeResourceFixture(t, dir, "memory.current", "1073741824\n")
	writeResourceFixture(t, dir, "memory.swap.max", "0\n")
	writeResourceFixture(t, dir, "memory.swap.current", "0\n")
	got, err := hostResourcesFromPaths(filepath.Join(dir, "meminfo"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope != "cgroup" || got.MemoryTotalBytes != 4294967296 || got.MemoryAvailableBytes != 3221225472 || got.SwapTotalBytes != 0 || !got.SwapLimitKnown || got.SwapUnlimited {
		t.Fatalf("cgroup resources = %+v", got)
	}
}

func TestHostResourcesFallsBackForUnlimitedCgroup(t *testing.T) {
	dir := t.TempDir()
	writeResourceFixture(t, dir, "meminfo", "MemTotal:       8388608 kB\nMemAvailable:   4194304 kB\nSwapTotal:      4194304 kB\nSwapFree:       2097152 kB\n")
	writeResourceFixture(t, dir, "memory.max", "max\n")
	got, err := hostResourcesFromPaths(filepath.Join(dir, "meminfo"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope != "host" || got.MemoryTotalBytes != 8*1024*1024*1024 || got.MemoryAvailableBytes != 4*1024*1024*1024 || got.SwapTotalBytes != 4*1024*1024*1024 {
		t.Fatalf("host resources = %+v", got)
	}
}

func writeResourceFixture(t *testing.T, dir, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}
