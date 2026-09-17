package boxruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func desktopSocket(assignment string) string {
	if WorkspaceRoot() != "/data" {
		assignment = fmt.Sprintf("%x", sha256.Sum256([]byte(assignment)))[:16]
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("vmbox-desktop-%d", os.Getuid()), assignment, "vnc.sock")
}

// DesktopEnabled is a read-only probe; it never installs or launches software.
func DesktopEnabled(ctx context.Context, assignment string) (bool, error) {
	if _, err := NativeSessions(ctx, assignment); err != nil {
		return false, err
	}
	for _, bin := range []string{"Xtigervnc", "openbox"} {
		if _, err := exec.LookPath(bin); err != nil {
			return false, nil
		}
	}
	return true, nil
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
	bins := []string{"Xtigervnc", "openbox", "chromium", "tint2", "pcmanfm", "xdg-user-dir", "xdotool", "xprintidle"}
	packages := []string{"tigervnc-standalone-server", "openbox", "chromium", "chromium-sandbox", "xterm", "dbus-x11", "fonts-dejavu-core", "tint2", "pcmanfm", "xdg-user-dirs", "adwaita-icon-theme", "xdotool", "xprintidle"}
	if blender {
		bins = append(bins, "blender", "pipx")
		packages = append(packages, "blender", "pipx", "python3-venv")
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
	for _, bin := range []string{"Xtigervnc", "openbox", "chromium"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("desktop components unavailable; enable the desktop worker image first")
		}
	}
	// A private tmux session owns the graphical processes, not the web viewer.
	// A session that outlived its desktop-run process reports as present while
	// no VNC socket will ever appear, so a stale one is replaced once rather
	// than timing out on every later attempt.
	for attempt := 0; attempt < 2; attempt++ {
		present := false
		for _, session := range inv.Sessions {
			if session.Name == "vmbox-desktop" {
				present = true
			}
		}
		if !present {
			if _, err = tmuxOutput(ctx, "if-shell", "-F", "#{==:#{@vmbox_assignment},"+assignment+"}", "new-session -d -s vmbox-desktop 'vmbox-runtime desktop-run "+assignment+"'", "display-message 'assignment changed'"); err != nil {
				return err
			}
		}
		ready, err := waitForDesktopSocket(ctx, assignment, 15*time.Second)
		if err != nil {
			return err
		}
		if ready {
			if err := ensureDesktopPanel(ctx, assignment); err != nil {
				return err
			}
			if err := ensureDesktopIcons(ctx, assignment); err != nil {
				return err
			}
			return EnsureDesktopTerminals(ctx, assignment)
		}
		if attempt > 0 || !present {
			return fmt.Errorf("desktop startup timed out; inspect vmbox-desktop session")
		}
		// Retire the stale session under the same assignment fence used to
		// create it, so a shared worker cannot lose another box's desktop.
		if _, err = tmuxOutput(ctx, "if-shell", "-F", "#{==:#{@vmbox_assignment},"+assignment+"}", "kill-session -t vmbox-desktop", "display-message 'assignment changed'"); err != nil {
			return err
		}
		if inv, err = NativeSessions(ctx, assignment); err != nil {
			return err
		}
	}
	return fmt.Errorf("desktop startup timed out; inspect vmbox-desktop session")
}

// waitForDesktopSocket reports whether the VNC socket accepted a connection
// within budget. A false return is a timeout, not a transport failure; only a
// cancelled caller returns an error.
func waitForDesktopSocket(ctx context.Context, assignment string, budget time.Duration) (bool, error) {
	deadline := time.NewTimer(budget)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			return false, nil
		case <-ticker.C:
			conn, err := net.DialTimeout("unix", desktopSocket(assignment), time.Second)
			if err == nil {
				conn.Close()
				return true, nil
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
	// A previous X server that died without cleaning up keeps the display
	// claimed, and Xtigervnc then exits immediately on every later attempt.
	if err := clearStaleDisplayLock(DesktopDisplay()); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	vnc := exec.CommandContext(ctx, "Xtigervnc", DesktopDisplay(), "-geometry", "1280x800", "-depth", "24", "-rfbport", "-1", "-rfbunixpath", socket, "-rfbunixmode", "0600", "-SecurityTypes", "None", "-AlwaysShared", "-nolisten", "tcp")
	// This process runs inside the vmbox-desktop tmux session, which tmux
	// destroys as soon as it exits. Anything Xtigervnc wrote to the pane dies
	// with it, so its diagnosis is captured here and returned instead.
	var vncLog lockedBuffer
	vnc.Stdout, vnc.Stderr = io.MultiWriter(os.Stdout, &vncLog), io.MultiWriter(os.Stderr, &vncLog)
	if err := vnc.Start(); err != nil {
		return fmt.Errorf("Xtigervnc could not start: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- vnc.Wait() }()
	defer func() { cancel(); <-exited }()
	ready := false
	for i := 0; i < 100 && !ready; i++ {
		conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case waitErr := <-exited:
			// Put the real reason in the error the controller reports.
			exited <- waitErr
			return fmt.Errorf("Xtigervnc exited before the desktop was ready (%v): %s", waitErr, vncLog.tail(400))
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !ready {
		return fmt.Errorf("VNC did not become ready: %s", vncLog.tail(400))
	}
	wm := exec.CommandContext(ctx, "openbox")
	wm.Env = append(os.Environ(), "DISPLAY="+DesktopDisplay())
	wm.Stderr = os.Stderr
	if err := wm.Start(); err != nil {
		return err
	}
	defer func() { cancel(); _ = wm.Wait() }()
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

// lockedBuffer collects child output while the readiness loop inspects it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) tail(limit int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	text := strings.TrimSpace(b.buf.String())
	if text == "" {
		return "no diagnostics from Xtigervnc"
	}
	if len(text) > limit {
		text = "…" + text[len(text)-limit:]
	}
	return strings.Join(strings.Fields(text), " ")
}

// clearStaleDisplayLock removes an X display lock whose owning process is gone.
// A lock held by a live process is left alone: that display really is in use.
func clearStaleDisplayLock(display string) error {
	number := strings.TrimPrefix(display, ":")
	if number == "" || strings.ContainsAny(number, "/\\.") {
		return nil
	}
	// X servers always place this in the real /tmp, not in TMPDIR.
	lock := filepath.Join("/tmp", ".X"+number+"-lock")
	data, err := os.ReadFile(lock)
	if err != nil {
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return nil
	}
	if process, err := os.FindProcess(pid); err == nil {
		if process.Signal(syscall.Signal(0)) == nil {
			return nil
		}
	}
	if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale display lock %s: %w", lock, err)
	}
	_ = os.Remove(filepath.Join("/tmp", ".X11-unix", "X"+number))
	return nil
}
