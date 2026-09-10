package boxruntime

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlenderInstallsDesktopAndRestoresWithoutCustomScript(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
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
	if err := InstallTools(context.Background(), home, []string{"blender"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := RestoreToolSetup(context.Background(), home, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{"blender", "tigervnc-standalone-server", "openbox", "firefox-esr"} {
		if strings.Count(string(data), pkg) != 2 {
			t.Fatalf("initial install and restore must include %s: %s", pkg, data)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte("#!/bin/sh\nexit 17\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := RestoreToolSetup(context.Background(), home, io.Discard); err == nil {
		t.Fatal("package failure must fail restoration")
	}
}

func TestBlenderAlreadyInstalledDoesNotInvokePackageManager(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for _, name := range []string{"blender", "Xtigervnc", "openbox", "firefox-esr"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := InstallTools(context.Background(), t.TempDir(), []string{"blender"}, io.Discard); err != nil {
		t.Fatal(err)
	}
}
