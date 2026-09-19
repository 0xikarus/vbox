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
	existing := desktopViewerWindows(ctx)
	for _, session := range inv.Sessions {
		if session.Name == "vmbox-desktop" || strings.HasPrefix(session.Name, "vmbox-internal-") {
			continue
		}
		// One viewer window per managed session. Re-creating it on every desktop
		// start left the vmbox-desktop session accumulating windows whose helper
		// exited immediately against the per-session lock, which burns worker
		// pids without ever putting a terminal on screen.
		window := desktopViewerWindowName(session.ID)
		if existing[window] {
			continue
		}
		command := "vmbox-runtime desktop-terminal " + shellQuote(assignment) + " " + shellQuote(session.ID) + " " + shellQuote(session.Incarnation)
		_, err = tmuxOutput(ctx, "if-shell", "-F", "#{==:#{@vmbox_assignment},"+assignment+"}", "new-window -d -t vmbox-desktop -n "+shellQuote(window)+" "+shellQuote(command), "display-message 'assignment changed'")
		if err != nil {
			return err
		}
		existing[window] = true
	}
	return nil
}

// desktopViewerWindowName keeps one stable tmux window per managed session so a
// repeated desktop start recognises the viewer it already created.
func desktopViewerWindowName(sessionID string) string {
	return "viewer-" + strings.TrimPrefix(sessionID, "$")
}

// desktopViewerWindows lists the vmbox-desktop windows. An absent session or
// tmux server simply means nothing has been created yet.
func desktopViewerWindows(ctx context.Context) map[string]bool {
	windows := map[string]bool{}
	output, err := tmuxOutput(ctx, "list-windows", "-t", "vmbox-desktop", "-F", "#{window_name}")
	if err != nil {
		return windows
	}
	for _, name := range strings.Split(string(output), "\n") {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			windows[trimmed] = true
		}
	}
	return windows
}

// raiseManagedDesktopTerminal puts the agent's terminal in front. openbox gives
// a new xterm no focus of its own, so without this the codex/claude/opencode
// session runs behind the browser and the desktop looks empty.
func raiseManagedDesktopTerminal(ctx context.Context) {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return
	}
	// xterm maps its window a moment after exec returns, so wait briefly for it.
	// Best effort throughout: a desktop without the window must not fail the
	// attach, which is why the terminal keeps running if this finds nothing.
	script := `n=0
while [ "$n" -lt 30 ]; do
  id=$(xdotool search --name '^vmbox managed session$' 2>/dev/null | tail -n1)
  if [ -n "$id" ]; then exec xdotool windowactivate --sync "$id"; fi
  n=$((n+1))
  sleep 0.1
done`
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Env = append(os.Environ(), "DISPLAY="+DesktopDisplay())
	_ = cmd.Run()
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
			// Another holder already owns this session's window. Surfacing it is
			// the point of the attach, so raise it instead of silently doing
			// nothing and leaving the agent hidden behind other windows.
			raiseManagedDesktopTerminal(ctx)
			return nil
		}
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	cmd := exec.CommandContext(ctx, "xterm", "-T", "vmbox managed session", "-geometry", "100x28-24+24", "-fa", "DejaVu Sans Mono", "-fs", "13", "-bg", "#300a24", "-fg", "#eeeeec", "-cr", "#f07746", "-e", "vmbox-runtime", "desktop-terminal-attach", assignment, id, incarnation)
	cmd.Env = append(os.Environ(), "DISPLAY="+DesktopDisplay())
	if err := cmd.Start(); err != nil {
		return err
	}
	raiseManagedDesktopTerminal(ctx)
	return cmd.Wait()
}
