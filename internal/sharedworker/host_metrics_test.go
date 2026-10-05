package sharedworker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHostMetricsUsesWorkspaceFilesystemAndCgroupCPU(t *testing.T) {
	root := t.TempDir()
	writeResourceFixture(t, root, "loadavg", "1.50 1.00 0.50 2/300 123\n")
	writeResourceFixture(t, root, "cpu.max", "200000 100000\n")
	writeResourceFixture(t, root, "cpu.stat", "usage_usec 1000\n")
	read := func(path string) (int64, int64, int64, error) {
		if path != filepath.Join(root, "workspaces") {
			t.Fatalf("wrong statfs path %q", path)
		}
		return 1000, 870, 110, nil
	}
	got, err := readHostMetrics(filepath.Join(root, "loadavg"), root, filepath.Join(root, "workspaces"), read, 8)
	if err != nil {
		t.Fatal(err)
	}
	if got.diskTotal != 1000 || got.diskUsed != 870 || got.diskFree != 110 || got.cores != 2 || got.load1 != 1.5 || got.scope != "cgroup" || got.percent != 75 {
		t.Fatalf("host metrics = %+v", got)
	}
	if got := cpuPercentFromDelta(500000, time.Second, 2); got != 25 {
		t.Fatalf("CPU delta percent = %v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "cpu.stat"), []byte("usage_usec 501000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if value, err := cpuUsageMicros(filepath.Join(root, "cpu.stat")); err != nil || value != 501000 {
		t.Fatalf("CPU usage=%d err=%v", value, err)
	}
}

func TestHostMetricsFallsBackToHostCores(t *testing.T) {
	root := t.TempDir()
	writeResourceFixture(t, root, "loadavg", "1.00 0.50 0.25 1/200 1\n")
	writeResourceFixture(t, root, "cpu.max", "max 100000\n")
	got, err := readHostMetrics(filepath.Join(root, "loadavg"), root, root, func(string) (int64, int64, int64, error) { return 1000, 100, 850, nil }, 4)
	if err != nil || got.scope != "host" || got.cores != 4 || got.percent != 25 {
		t.Fatalf("host metrics=%+v err=%v", got, err)
	}
}
