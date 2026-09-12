package boxruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopIconsPreserveCustomLaunchers(t *testing.T) {
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock required")
	}
	home, bin := t.TempDir(), t.TempDir()
	for name, body := range map[string]string{
		"xdg-user-dir":         "printf '%s/Custom Desktop\\n' \"$HOME\"",
		"xdg-user-dirs-update": "exit 0",
		"firefox-esr":          "exit 0", "xterm": "exit 0", "pcmanfm": "exit 0", "blender": "exit 0",
		"dbus-run-session": "printf '%s\\n' \"$DISPLAY\" \"$*\" >\"$HOME/started\"",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	run := func() {
		t.Helper()
		if out, err := exec.Command("sh", "-c", desktopIconsScript).CombinedOutput(); err != nil {
			t.Fatalf("icons: %v: %s", err, out)
		}
	}
	run()
	for _, app := range []string{"firefox-esr", "xterm", "pcmanfm", "blender"} {
		path := filepath.Join(home, "Custom Desktop", "vmbox-"+app+".desktop")
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), "Exec="+app+"\n") {
			t.Fatalf("launcher %s: %s, %v", app, data, err)
		}
	}
	custom := filepath.Join(home, "Custom Desktop", "vmbox-firefox-esr.desktop")
	if err := os.WriteFile(custom, []byte("owner customization\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run()
	data, _ := os.ReadFile(custom)
	if string(data) != "owner customization\n" {
		t.Fatal("overwrote owner launcher")
	}
	started, _ := os.ReadFile(filepath.Join(home, "started"))
	if string(started) != ":99\n-- pcmanfm --profile=vmbox --desktop\n" {
		t.Fatalf("unexpected desktop start: %s", started)
	}
}
