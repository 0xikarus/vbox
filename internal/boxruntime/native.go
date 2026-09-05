package boxruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

type nativeCaptureBuffer struct {
	bytes.Buffer
	partial bool
}

func (b *nativeCaptureBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (1 << 20) - b.Len()
	if n > remaining {
		b.partial = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func captureNativePane(ctx context.Context, pane string) ([]byte, bool, error) {
	cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-t", pane, "-S", "-200")
	var out nativeCaptureBuffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.Bytes(), out.partial, err
}

var nativeID = regexp.MustCompile(`^\$[0-9]+$`)
var nativeAssignment = regexp.MustCompile(`^[a-f0-9]{64}$`)
var nativeServer = regexp.MustCompile(`^[a-f0-9]{24}$`)

// SetNativeAssignment is called by allocation, never by an attaching client.
func SetNativeAssignment(ctx context.Context, assignment string) error {
	if !nativeAssignment.MatchString(assignment) {
		return fmt.Errorf("invalid assignment")
	}
	_, err := tmuxOutput(ctx, "start-server", ";", "set-option", "-g", "exit-empty", "off", ";", "set-option", "-g", "@vmbox_assignment", assignment, ";", "set-option", "-goq", "@vmbox_server_incarnation", ID(""))
	return err
}

func NativeSessions(ctx context.Context, expected string) (v1.SessionInventory, error) {
	out := v1.SessionInventory{Assignment: expected, State: "live", ObservedAt: time.Now().UTC(), Sessions: []v1.Session{}}
	if !nativeAssignment.MatchString(expected) {
		return out, fmt.Errorf("invalid assignment")
	}
	fence, err := tmuxOutput(ctx, "show-option", "-gv", "@vmbox_assignment")
	if err != nil || strings.TrimSpace(string(fence)) != expected {
		return out, fmt.Errorf("worker assignment unavailable or changed")
	}
	server, err := tmuxOutput(ctx, "show-option", "-gv", "@vmbox_server_incarnation")
	serverID := strings.TrimSpace(string(server))
	if err != nil || !nativeServer.MatchString(serverID) {
		return out, fmt.Errorf("server incarnation unavailable; enable native sessions")
	}
	ids, err := tmuxOutput(ctx, "list-sessions", "-F", "#{session_id}")
	if err != nil && strings.HasSuffix(strings.TrimSpace(err.Error()), ": no sessions") {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	budget := 128
	for _, id := range strings.Fields(string(ids)) {
		if len(out.Sessions) >= 64 {
			out.Partial = true
			break
		}
		if !nativeID.MatchString(id) {
			return out, fmt.Errorf("invalid runtime session ID")
		}
		name, err := tmuxOutput(ctx, "display-message", "-p", "-t", id, "#{session_name}")
		if err != nil {
			return out, err
		}
		s := v1.Session{ID: id, Name: strings.TrimSuffix(string(name), "\n"), Incarnation: expected + ":" + serverID + ":" + id}
		panes, err := tmuxOutput(ctx, "list-panes", "-s", "-t", id, "-F", "#{pane_id}")
		if err != nil {
			return out, err
		}
		h := sha256.New()
		for _, pane := range strings.Fields(string(panes)) {
			s.Panes++
			if budget == 0 {
				s.Partial = true
				out.Partial = true
				continue
			}
			budget--
			metadata, err := tmuxOutput(ctx, "display-message", "-p", "-t", pane, "#{window_id}:#{pane_id}:#{pane_dead}:#{pane_current_command}:#{cursor_x}:#{cursor_y}:#{pane_width}:#{pane_height}")
			if err != nil {
				return out, err
			}
			h.Write(metadata)
			screen, partial, err := captureNativePane(ctx, pane)
			if err != nil {
				return out, err
			}
			if partial {
				s.Partial = true
				out.Partial = true
			}
			fmt.Fprintf(h, "%s:%d:", pane, len(screen))
			h.Write(screen)
		}
		s.Fingerprint = fmt.Sprintf("%x", h.Sum(nil))
		out.Sessions = append(out.Sessions, s)
	}
	return out, nil
}

func NativeAttach(ctx context.Context, assignment, id, incarnation string) error {
	if !nativeAssignment.MatchString(assignment) || !nativeID.MatchString(id) {
		return fmt.Errorf("invalid attach identity")
	}
	parts := strings.Split(incarnation, ":")
	if len(parts) != 3 || parts[0] != assignment || parts[2] != id || !nativeServer.MatchString(parts[1]) {
		return fmt.Errorf("invalid session incarnation")
	}
	fence, err := tmuxOutput(ctx, "show-option", "-gv", "@vmbox_assignment")
	if err != nil || strings.TrimSpace(string(fence)) != assignment {
		return fmt.Errorf("assignment changed; reconnect")
	}
	server, err := tmuxOutput(ctx, "show-option", "-gv", "@vmbox_server_incarnation")
	if err != nil || strings.TrimSpace(string(server)) != parts[1] {
		return fmt.Errorf("tmux server was recreated; reconnect")
	}
	// The check and attach execute in the same tmux server command queue. IDs
	// are never reused within a server. A new allocation installs a new fence.
	condition := "#{&&:#{==:#{@vmbox_assignment}," + assignment + "},#{==:#{@vmbox_server_incarnation}," + parts[1] + "}}"
	cmd := exec.CommandContext(ctx, "tmux", "if-shell", "-F", "-t", id, condition, "attach-session -t '"+id+"'", "display-message 'assignment changed; reconnect'")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func NativeWelcome(ctx context.Context, assignment string) error {
	inv, err := NativeSessions(ctx, assignment)
	if err != nil {
		return err
	}
	if len(inv.Sessions) != 0 {
		return fmt.Errorf("sessions already exist; select one")
	}
	_, err = tmuxOutput(ctx, "if-shell", "-F", "#{==:#{@vmbox_assignment},"+assignment+"}", "new-session -d -s vmbox -c /data/workspace 'vmbox-runtime welcome'", "display-message 'assignment changed'")
	return err
}
