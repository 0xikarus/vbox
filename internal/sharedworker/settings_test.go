package sharedworker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

// A 4-core host with 7.8 GiB RAM and 4 GiB swap, like a small VPS.
var smallHost = provider.WorkerSpecs{CPUs: 4, MemoryBytes: 7800 << 20, SwapBytes: 4 << 30, DiskTotalBytes: 80 << 30, DiskFreeBytes: 40 << 30}

func openWithSpecs(t *testing.T, root string, capacity int, runtime Runtime, specs provider.WorkerSpecs) *Store {
	t.Helper()
	store, err := Open(root, "account", capacity, runtime)
	if err != nil {
		t.Fatal(err)
	}
	store.Specs = func() (provider.WorkerSpecs, error) { return specs, nil }
	t.Cleanup(func() { store.Close() })
	return store
}

func createSlot(t *testing.T, store *Store, name string) error {
	t.Helper()
	_, err := store.Create(provider.CreateRequest{Name: name, Owner: provider.Owner{AccountID: "account", BoxID: name}})
	return err
}

func TestSettingsSeedFromStartupCapacityOnlyOnce(t *testing.T) {
	root := t.TempDir()
	store := openWithSpecs(t, root, 2, &fakeRuntime{}, smallHost)
	config, err := store.WorkerConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Settings.Slots != 2 || config.Settings.Revision != 1 || config.Settings.BoxDefaults != legacyBoxDefaults {
		t.Fatalf("unexpected seed: %+v", config.Settings)
	}
	config.Settings.Slots = 5
	if _, err := store.SetSettings(config.Settings); err != nil {
		t.Fatal(err)
	}
	store.Close()
	reopened := openWithSpecs(t, root, 2, &fakeRuntime{}, smallHost)
	if got := reopened.SlotCapacity(); got != 5 {
		t.Fatalf("startup capacity overrode saved settings: %d", got)
	}
}

func TestSettingsSeedWithoutStartupCapacity(t *testing.T) {
	if got := openWithSpecs(t, t.TempDir(), 0, &fakeRuntime{}, smallHost).SlotCapacity(); got != 1 {
		t.Fatalf("default slots = %d, want 1", got)
	}
}

func TestLegacyStateSeedsSettingsFromRetainedSlots(t *testing.T) {
	root := t.TempDir()
	store := openWithSpecs(t, root, 3, &fakeRuntime{}, smallHost)
	for _, name := range []string{"slot-a", "slot-b"} {
		if err := createSlot(t, store, name); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	path := filepath.Join(root, ".shared-worker", "state.json")
	var state map[string]json.RawMessage
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	delete(state, "settings")
	data, _ = json.Marshal(state)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got := openWithSpecs(t, root, 0, &fakeRuntime{}, smallHost).SlotCapacity(); got != 2 {
		t.Fatalf("legacy seed = %d, want retained 2", got)
	}
}

func TestSetSettingsChangesCapacityLive(t *testing.T) {
	store := openWithSpecs(t, t.TempDir(), 1, &fakeRuntime{}, smallHost)
	if err := createSlot(t, store, "slot-a"); err != nil {
		t.Fatal(err)
	}
	if err := createSlot(t, store, "slot-b"); err == nil {
		t.Fatal("capacity was not enforced")
	}
	config, _ := store.WorkerConfig()
	config.Settings.Slots = 2
	if _, err := store.SetSettings(config.Settings); err != nil {
		t.Fatal(err)
	}
	if err := createSlot(t, store, "slot-b"); err != nil {
		t.Fatalf("raised capacity not applied without restart: %v", err)
	}
}

func TestSetSettingsBounds(t *testing.T) {
	store := openWithSpecs(t, t.TempDir(), 3, &fakeRuntime{}, smallHost)
	for _, name := range []string{"slot-a", "slot-b"} {
		if err := createSlot(t, store, name); err != nil {
			t.Fatal(err)
		}
	}
	config, _ := store.WorkerConfig()
	if config.Limits.MinSlots != 2 || config.Limits.MaxSlots != 7 {
		t.Fatalf("limits = %+v, want 2–7 slots on a 7.8 GiB host", config.Limits)
	}
	for _, tc := range []struct {
		name     string
		settings provider.WorkerSettings
		want     string
	}{
		{"stale revision", provider.WorkerSettings{Revision: 9, Slots: 3}, "revision conflict"},
		{"below slots in use", provider.WorkerSettings{Revision: 1, Slots: 1}, "2 slots in use"},
		{"above machine", provider.WorkerSettings{Revision: 1, Slots: 8}, "at most 7 slots"},
	} {
		if _, err := store.SetSettings(tc.settings); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	if got := store.SlotCapacity(); got != 3 {
		t.Fatalf("rejected settings changed capacity to %d", got)
	}
}

func TestSetSettingsIgnoresBoxDefaultsWithoutContainers(t *testing.T) {
	store := openWithSpecs(t, t.TempDir(), 1, &fakeRuntime{}, smallHost)
	config, err := store.SetSettings(provider.WorkerSettings{Revision: 1, Slots: 2, BoxDefaults: provider.BoxLimits{CPU: 99}})
	if err != nil {
		t.Fatal(err)
	}
	if config.Limits.PerBoxLimits || config.Settings.BoxDefaults != legacyBoxDefaults || config.Settings.Revision != 2 {
		t.Fatalf("unexpected config: %+v", config)
	}
}

func TestContainerBoxDefaultsAreBoundedByMachine(t *testing.T) {
	root := t.TempDir()
	store := openWithSpecs(t, root, 2, &LinuxRuntime{Root: root, Container: &ContainerRuntime{}}, smallHost)
	config, _ := store.WorkerConfig()
	want := provider.BoxLimits{CPU: 4, MemoryMiB: 7 * 1024, SwapMiB: 4 * 1024}
	if !config.Limits.PerBoxLimits || config.Limits.BoxMax != want {
		t.Fatalf("box max = %+v, want %+v", config.Limits.BoxMax, want)
	}
	for _, box := range []provider.BoxLimits{
		{CPU: 4.5, MemoryMiB: 2048, SwapMiB: 0},
		{CPU: 0.75, MemoryMiB: 2048, SwapMiB: 0},
		{CPU: 1, MemoryMiB: 8192, SwapMiB: 0},
		{CPU: 1, MemoryMiB: 1536, SwapMiB: 0},
		{CPU: 1, MemoryMiB: 2048, SwapMiB: 5120},
	} {
		if _, err := store.SetSettings(provider.WorkerSettings{Revision: 1, Slots: 2, BoxDefaults: box}); err == nil {
			t.Errorf("accepted out-of-bounds defaults %+v", box)
		}
	}
	// Six slots of 2 GiB on a 7.8 GiB host overcommits memory; that is allowed.
	config, err := store.SetSettings(provider.WorkerSettings{Revision: 1, Slots: 6, BoxDefaults: provider.BoxLimits{CPU: 1.5, MemoryMiB: 2048, SwapMiB: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if !config.Overcommitted || config.Settings.BoxDefaults.CPU != 1.5 {
		t.Fatalf("unexpected config: %+v", config)
	}
	limits, err := store.newBoxLimits(provider.Resources{})
	if err != nil || limits != config.Settings.BoxDefaults {
		t.Fatalf("new box limits = %+v, %v; want defaults", limits, err)
	}
	if _, err := store.newBoxLimits(provider.Resources{MemoryMiB: 16 << 10}); err == nil {
		t.Fatal("new box above machine memory accepted")
	}
}

func TestLimitsRoundNominalSizes(t *testing.T) {
	limits := limitsFor(provider.WorkerSpecs{CPUs: 8, MemoryBytes: 16553222144, SwapBytes: 4294963200}, 0, true)
	if limits.BoxMax.SwapMiB != 4096 || limits.BoxMax.MemoryMiB != 15*1024 || limits.MaxSlots != 15 {
		t.Fatalf("limits = %+v", limits)
	}
}

func TestNoSwapHostLimitsSwapToZero(t *testing.T) {
	host := smallHost
	host.SwapBytes = 0
	limits := limitsFor(host, 0, true)
	if limits.BoxMax.SwapMiB != 0 {
		t.Fatalf("swap max = %d", limits.BoxMax.SwapMiB)
	}
	if err := limits.CheckBox(provider.BoxLimits{CPU: 1, MemoryMiB: 2048, SwapMiB: 1024}); err == nil || !strings.Contains(err.Error(), "no swap") {
		t.Fatalf("err = %v", err)
	}
	if got := clampBoxLimits(legacyBoxDefaults, limits); got.SwapMiB != 0 {
		t.Fatalf("defaults not clamped: %+v", got)
	}
}

func TestMachineSpecsFromPaths(t *testing.T) {
	dir := t.TempDir()
	meminfo := filepath.Join(dir, "meminfo")
	cpuMax := filepath.Join(dir, "cpu.max")
	os.WriteFile(meminfo, []byte("MemTotal: 8000000 kB\nMemAvailable: 4000000 kB\nSwapTotal: 1048576 kB\nSwapFree: 1048576 kB\n"), 0600)
	os.WriteFile(cpuMax, []byte("50000 100000\n"), 0600)
	specs, err := machineSpecsFromPaths(meminfo, cpuMax, dir)
	if err != nil {
		t.Fatal(err)
	}
	if specs.CPUs != min(0.5, float64(runtime.NumCPU())) || specs.MemoryBytes != 8000000*1024 || specs.SwapBytes != 1<<30 || specs.DiskTotalBytes <= 0 {
		t.Fatalf("unexpected specs: %+v", specs)
	}
	os.WriteFile(cpuMax, []byte("max 100000\n"), 0600)
	if specs, _ = machineSpecsFromPaths(meminfo, cpuMax, dir); specs.CPUs != float64(runtime.NumCPU()) {
		t.Fatalf("unlimited cpu.max not ignored: %+v", specs)
	}
}
