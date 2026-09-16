package boxruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChromiumNoSandboxUsesContainerBoundaryAsDefault(t *testing.T) {
	for _, test := range []struct {
		setting   string
		container bool
		want      bool
	}{
		{setting: "true", want: true},
		{setting: "false", container: true, want: false},
		{container: true, want: true},
		{want: false},
	} {
		if got := chromiumNoSandbox(test.setting, test.container); got != test.want {
			t.Fatalf("setting=%q container=%t: got %t, want %t", test.setting, test.container, got, test.want)
		}
	}
}

func TestDesktopIconsPreserveCustomLaunchers(t *testing.T) {
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock required")
	}
	home, bin := t.TempDir(), t.TempDir()
	for name, body := range map[string]string{
		"xdg-user-dir":         "printf '%s/Custom Desktop\\n' \"$HOME\"",
		"xdg-user-dirs-update": "exit 0",
		"chromium":             "exit 0", "xterm": "exit 0", "pcmanfm": "exit 0", "blender": "exit 0",
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
	config := filepath.Join(home, ".config", "libfm", "libfm.conf")
	configData, err := os.ReadFile(config)
	if err != nil || string(configData) != "[config]\nquick_exec=1\n" {
		t.Fatalf("libfm launcher behavior: %q, %v", configData, err)
	}
	for _, app := range []string{"chromium", "xterm", "pcmanfm", "blender"} {
		path := filepath.Join(home, "Custom Desktop", "vmbox-"+app+".desktop")
		data, err := os.ReadFile(path)
		command := app
		if app == "chromium" {
			command = "vmbox-runtime desktop-browser"
		} else if app == "xterm" {
			command = `xterm -fa "DejaVu Sans Mono" -fs 13 -bg "#300a24" -fg "#eeeeec" -cr "#f07746"`
		}
		if err != nil || !strings.Contains(string(data), "Exec="+command+"\n") {
			t.Fatalf("launcher %s: %s, %v", app, data, err)
		}
	}
	custom := filepath.Join(home, "Custom Desktop", "vmbox-chromium.desktop")
	if err := os.WriteFile(custom, []byte("owner customization\n"), 0600); err != nil {
		t.Fatal(err)
	}
	legacyTerminal := filepath.Join(home, "Custom Desktop", "vmbox-xterm.desktop")
	if err := os.WriteFile(legacyTerminal, []byte("[Desktop Entry]\nType=Application\nName=Terminal\nExec=xterm\nIcon=utilities-terminal\nTerminal=false\nStartupNotify=true\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("[config]\nthumbnail_max=4096\nquick_exec=0\n[ui]\nbig_icon_size=64\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run()
	terminalData, err := os.ReadFile(legacyTerminal)
	if err != nil || !strings.Contains(string(terminalData), `Exec=xterm -fa "DejaVu Sans Mono" -fs 13 -bg "#300a24"`) {
		t.Fatalf("legacy terminal launcher was not upgraded: %q, %v", terminalData, err)
	}
	configData, err = os.ReadFile(config)
	if err != nil || string(configData) != "[config]\nthumbnail_max=4096\nquick_exec=1\n[ui]\nbig_icon_size=64\n" {
		t.Fatalf("libfm preferences were not preserved: %q, %v", configData, err)
	}
	data, _ := os.ReadFile(custom)
	if string(data) != "owner customization\n" {
		t.Fatal("overwrote owner launcher")
	}
	started, _ := os.ReadFile(filepath.Join(home, "started"))
	if string(started) != ":99\n-- pcmanfm --profile=vmbox --desktop\n" {
		t.Fatalf("unexpected desktop start: %s", started)
	}
}
