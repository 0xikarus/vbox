package boxruntime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// prepareManagedDesktop registers agent chat on every worker image, then makes
// desktop-enabled sessions visible from their first launch. The existing
// assignment is authoritative; this helper never allocates compute or installs
// packages.
func prepareManagedDesktop(ctx context.Context, agent string) (string, error) {
	if err := RegisterDesktopMCP(ctx, os.Getenv("HOME"), agent); err != nil {
		return "", err
	}
	if _, err := exec.LookPath("Xtigervnc"); err != nil {
		return "", nil
	}
	fence, err := tmuxOutput(ctx, "show-option", "-gv", "@vmbox_assignment")
	if err != nil {
		return "", fmt.Errorf("desktop startup requires a worker assignment")
	}
	assignment := strings.TrimSpace(string(fence))
	if _, err := NativeSessions(ctx, assignment); err != nil {
		return "", err
	}
	if err := StartDesktop(ctx, assignment); err != nil {
		return "", err
	}
	return assignment, nil
}

// EnsureDesktopTerminals attaches visible viewers to existing managed sessions.
// It never starts another agent or replays a prompt.
func EnsureDesktopTerminals(ctx context.Context, assignment string) error {
	inv, err := NativeSessions(ctx, assignment)
	if err != nil {
		return err
	}
	for _, session := range inv.Sessions {
		if session.Name == "vmbox-desktop" || strings.HasPrefix(session.Name, "vmbox-internal-") {
			continue
		}
		command := "vmbox-runtime desktop-terminal " + shellQuote(assignment) + " " + shellQuote(session.ID) + " " + shellQuote(session.Incarnation)
		_, err = tmuxOutput(ctx, "if-shell", "-F", "#{==:#{@vmbox_assignment},"+assignment+"}", "new-window -d -t vmbox-desktop -n viewer "+shellQuote(command), "display-message 'assignment changed'")
		if err != nil {
			return err
		}
	}
	return nil
}

// RunDesktopTerminal holds a per-session lock for the lifetime of xterm so repeat
// desktop attachments cannot accumulate windows. Closing xterm detaches tmux.
func RunDesktopTerminal(ctx context.Context, root, assignment, id, incarnation string) error {
	inv, err := NativeSessions(ctx, assignment)
	if err != nil {
		return err
	}
	valid := false
	for _, s := range inv.Sessions {
		if s.ID == id && s.Incarnation == incarnation && s.Name != "vmbox-desktop" {
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("desktop terminal session changed")
	}
	dir := filepath.Dir(desktopSocket(assignment))
	lock, err := os.OpenFile(filepath.Join(dir, "terminal-"+strings.TrimPrefix(id, "$")+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if err == unix.EWOULDBLOCK {
			return nil
		}
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	cmd := exec.CommandContext(ctx, "xterm", "-T", "vmbox managed session", "-geometry", "100x28+24+24", "-fa", "DejaVu Sans Mono", "-fs", "13", "-bg", "#300a24", "-fg", "#eeeeec", "-cr", "#f07746", "-e", "vmbox-runtime", "desktop-terminal-attach", assignment, id, incarnation)
	cmd.Env = append(os.Environ(), "DISPLAY="+DesktopDisplay())
	return cmd.Run()
}
