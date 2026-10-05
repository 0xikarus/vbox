package boxruntime

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolInstallDiskAdmissionChecksHomeAndRoot(t *testing.T) {
	home := "/data/home"
	read := func(path string) (int64, int64, error) {
		if path == home {
			return 100, 6, nil
		}
		return 100, 4, nil
	}
	if err := checkInstallDiskSpace(home, read); err == nil || !strings.Contains(err.Error(), "insufficient disk space") || !strings.Contains(err.Error(), "on /") {
		t.Fatalf("root writable layer was not checked: %v", err)
	}
	read = func(path string) (int64, int64, error) {
		if path == home {
			return 100, 4, nil
		}
		return 100, 6, nil
	}
	if err := checkInstallDiskSpace(home, read); err == nil || !strings.Contains(err.Error(), "on "+home) {
		t.Fatalf("persistent home was not checked: %v", err)
	}
	if err := checkInstallDiskSpace(home, func(string) (int64, int64, error) { return 100, 5, nil }); err != nil {
		t.Fatalf("5%% boundary rejected: %v", err)
	}
	problem := errors.New("statfs failed")
	if err := checkInstallDiskSpace(home, func(string) (int64, int64, error) { return 0, 0, problem }); !errors.Is(err, problem) {
		t.Fatalf("unavailable disk check = %v", err)
	}
}

func TestInstallDiskCheckUsesExistingVolumeForNewHome(t *testing.T) {
	if total, free, err := readInstallDisk(filepath.Join(t.TempDir(), "new-home")); err != nil || total <= 0 || free <= 0 {
		t.Fatalf("new home disk total=%d free=%d err=%v", total, free, err)
	}
}
