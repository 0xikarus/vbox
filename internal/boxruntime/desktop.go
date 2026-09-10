package boxruntime

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func desktopSocket(assignment string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("vmbox-desktop-%d", os.Getuid()), assignment, "vnc.sock")
}

// EnableDesktop changes only system packages on the current worker. It neither
// restarts compute nor modifies workspace credentials or persistent files.
func EnableDesktop(ctx context.Context, assignment string) error {
	if _, err := NativeSessions(ctx, assignment); err != nil {
		return err
	}
	return installDesktopPackages(ctx, os.Stdout, false)
}

// Package installation is also used before a new box has a tmux assignment.
func installDesktopPackages(ctx context.Context, progress io.Writer, blender bool) error {
	bins := []string{"Xtigervnc", "openbox", "firefox-esr"}
	packages := []string{"tigervnc-standalone-server", "openbox", "firefox-esr", "xterm", "dbus-x11", "fonts-dejavu-core"}
	if blender {
		bins = append(bins, "blender")
		packages = append(packages, "blender")
	}
	installed := true
	for _, bin := range bins {
		if _, err := exec.LookPath(bin); err != nil {
			installed = false
		}
	}
	if installed {
		return nil
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return fmt.Errorf("desktop enablement requires a Debian-compatible worker image")
	}
	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "-o", "DPkg::Lock::Timeout=30"}, args...)...)
		cmd.Stdout, cmd.Stderr = progress, progress
		return cmd.Run()
	}
	if err := run("update"); err != nil {
		return fmt.Errorf("desktop package index update failed: %w", err)
	}
	// A timed-out or interrupted installation can leave dependencies unpacked.
	// Repair only when apt reports a broken dependency graph, never by removing
	// existing packages. The caller's deadline also bounds repair.
	if err := run("check"); err != nil {
		fmt.Fprintln(progress, "Repairing interrupted package dependencies (package removal forbidden)…")
		if err := run("--fix-broken", "--no-remove", "install", "-y", "--no-install-recommends"); err != nil {
			return fmt.Errorf("desktop dependency repair failed: %w; inspect worker package manager diagnostics", err)
		}
	}
	if err := run(append([]string{"install", "--no-remove", "-y", "--no-install-recommends"}, packages...)...); err != nil {
		return fmt.Errorf("desktop package installation failed: %w; inspect worker package manager diagnostics", err)
	}
	return nil
}

func StartDesktop(ctx context.Context, assignment string) error {
	inv, err := NativeSessions(ctx, assignment)
	if err != nil {
		return err
	}
	for _, bin := range []string{"Xtigervnc", "openbox", "firefox-esr"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("desktop components unavailable; enable the desktop worker image first")
		}
	}
	// A private tmux session owns the graphical processes, not the web viewer.
	exists := false
	for _, session := range inv.Sessions {
		if session.Name == "vmbox-desktop" {
			exists = true
		}
	}
	if !exists {
		_, err = tmuxOutput(ctx, "if-shell", "-F", "#{==:#{@vmbox_assignment},"+assignment+"}", "new-session -d -s vmbox-desktop 'vmbox-runtime desktop-run "+assignment+"'", "display-message 'assignment changed'")
		if err != nil {
			return err
		}
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("desktop startup timed out; inspect vmbox-desktop session")
		case <-ticker.C:
			conn, err := net.DialTimeout("unix", desktopSocket(assignment), time.Second)
			if err == nil {
				conn.Close()
				return nil
			}
		}
	}
}

func RunDesktop(ctx context.Context, assignment string) error {
	if _, err := NativeSessions(ctx, assignment); err != nil {
		return err
	}
	if os.Getuid() == 0 {
		return fmt.Errorf("desktop must run as the workspace user")
	}
	socket := desktopSocket(assignment)
	if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	vnc := exec.CommandContext(ctx, "Xtigervnc", ":99", "-geometry", "1280x800", "-depth", "24", "-rfbport", "-1", "-rfbunixpath", socket, "-rfbunixmode", "0600", "-SecurityTypes", "None", "-AlwaysShared", "-nolisten", "tcp")
	vnc.Stdout, vnc.Stderr = os.Stdout, os.Stderr
	if err := vnc.Start(); err != nil {
		return err
	}
	defer func() { cancel(); _ = vnc.Wait() }()
	ready := false
	for i := 0; i < 100; i++ {
		conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !ready {
		return fmt.Errorf("VNC did not become ready")
	}
	wm := exec.CommandContext(ctx, "openbox")
	wm.Env = append(os.Environ(), "DISPLAY=:99")
	wm.Stderr = os.Stderr
	if err := wm.Start(); err != nil {
		return err
	}
	defer func() { cancel(); _ = wm.Wait() }()
	browser := exec.CommandContext(ctx, "firefox-esr", "--no-remote", "about:blank")
	browser.Env = append(os.Environ(), "DISPLAY=:99")
	browser.Stderr = os.Stderr
	if err := browser.Start(); err != nil {
		return err
	}
	go func() { _ = browser.Wait() }()
	<-ctx.Done()
	return ctx.Err()
}

func StreamDesktop(ctx context.Context, assignment string, input io.Reader, output io.Writer) error {
	if _, err := NativeSessions(ctx, assignment); err != nil {
		return err
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", desktopSocket(assignment))
	if err != nil {
		return fmt.Errorf("desktop unavailable; start desktop first")
	}
	defer conn.Close()
	done := make(chan error, 2)
	go func() { _, err := io.Copy(conn, input); done <- err }()
	go func() { _, err := io.Copy(output, conn); done <- err }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}
