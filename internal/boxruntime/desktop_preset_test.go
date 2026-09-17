package boxruntime

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopPresetRetainedWithoutBlender(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	log := filepath.Join(t.TempDir(), "packages")
	t.Setenv("PATH", bin)
	t.Setenv("PACKAGE_LOG", log)
	for name, script := range map[string]string{
		"apt-get": "#!/bin/sh\nexit 0\n",
		"sudo":    "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PACKAGE_LOG\"\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := RestoreToolSetup(context.Background(), home, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("unselected desktop attempted installation")
	}
	if err := InstallTools(context.Background(), home, []string{"desktop"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := RestoreToolSetup(context.Background(), home, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "install --no-remove") != 2 || !strings.Contains(string(data), "chromium") || strings.Contains(string(data), "blender") {
		t.Fatalf("unexpected desktop installation/restore: %s", data)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "vmbox", "blender-enabled")); !os.IsNotExist(err) {
		t.Fatal("desktop preset enabled Blender")
	}
}
