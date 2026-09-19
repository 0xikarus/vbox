package boxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/desktop"
	"golang.org/x/sys/unix"
)

type DesktopAction struct {
	Action string   `json:"action"`
	X      int      `json:"x"`
	Y      int      `json:"y"`
	ToX    int      `json:"toX"`
	ToY    int      `json:"toY"`
	Button int      `json:"button,omitempty"`
	Count  int      `json:"count,omitempty"`
	Text   string   `json:"text,omitempty"`
	Keys   []string `json:"keys,omitempty"`
}

const desktopDoubleClickDelay = 120 * time.Millisecond

func desktopInputCommand(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "xdotool", args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+DesktopDisplay())
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("desktop input failed")
	}
	return out, nil
}

func validateDesktopAction(a DesktopAction) error {
	point := func(x, y int) bool { return x >= 0 && x < defaultDesktopWidth && y >= 0 && y < defaultDesktopHeight }
	switch a.Action {
	case "pause", "resume":
		return nil
	case "move", "click", "scroll", "drag":
		if !point(a.X, a.Y) || (a.Action == "drag" && !point(a.ToX, a.ToY)) {
			return fmt.Errorf("desktop coordinates out of bounds")
		}
		if a.Button < 0 || a.Button > 3 || a.Count < 0 || a.Count > 20 {
			return fmt.Errorf("invalid button/count")
		}
		if a.Action == "scroll" && a.Text != "up" && a.Text != "down" && a.Text != "left" && a.Text != "right" {
			return fmt.Errorf("invalid scroll direction")
		}
	case "type":
		if a.Text == "" || len(a.Text) > 16384 || strings.ContainsRune(a.Text, 0) {
			return fmt.Errorf("text must contain 1–16384 bytes without NUL")
		}
	case "key":
		if len(a.Keys) == 0 || len(a.Keys) > 5 {
			return fmt.Errorf("provide 1–5 keys")
		}
		for _, key := range a.Keys {
			if len(key) == 1 && ((key[0] >= 'a' && key[0] <= 'z') || (key[0] >= '0' && key[0] <= '9')) {
				continue
			}
			switch key {
			case "ctrl", "alt", "shift", "super", "Return", "Escape", "Tab", "BackSpace", "Delete", "Up", "Down", "Left", "Right", "Home", "End", "Page_Up", "Page_Down", "space":
			default:
				return fmt.Errorf("unsupported key")
			}
		}
	default:
		return fmt.Errorf("unsupported desktop action")
	}
	return nil
}

func desktopClicks(ctx context.Context, count, button int, check func() error, run func(...string) error) error {
	for i := 0; i < count; i++ {
		if i > 0 {
			timer := time.NewTimer(desktopDoubleClickDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := check(); err != nil {
			return err
		}
		if err := run("click", strconv.Itoa(button)); err != nil {
			return err
		}
	}
	return nil
}

// DesktopInput serializes managed actions across MCP processes. The pause marker
// is set before waiting on the lock, so a move already in flight observes takeover.
func DesktopInput(ctx context.Context, assignment string, a DesktopAction) error {
	if err := validateDesktopAction(a); err != nil {
		return err
	}
	if _, err := NativeSessions(ctx, assignment); err != nil {
		return err
	}
	dir := filepath.Dir(desktopSocket(assignment))
	if _, err := os.Stat(desktopSocket(assignment)); err != nil {
		return fmt.Errorf("desktop unavailable")
	}
	paused := filepath.Join(dir, "input-paused")
	if a.Action == "pause" {
		if err := os.WriteFile(paused, []byte("paused\n"), 0600); err != nil {
			return err
		}
	}
	lock, err := os.OpenFile(filepath.Join(dir, "input.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if a.Action == "pause" {
		return nil
	}
	if a.Action == "resume" {
		err := os.Remove(paused)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := os.Stat(paused); err == nil {
			return fmt.Errorf("desktop input paused for human takeover")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fence, err := tmuxOutput(ctx, "show-option", "-gv", "@vmbox_assignment")
		if err != nil || strings.TrimSpace(string(fence)) != assignment {
			return fmt.Errorf("assignment changed")
		}
		return nil
	}
	if err = check(); err != nil {
		return err
	}
	run := func(args ...string) error { _, err := desktopInputCommand(ctx, "", args...); return err }
	move := func(target desktop.Point, bend float64) error {
		out, err := desktopInputCommand(ctx, "", "getmouselocation", "--shell")
		if err != nil {
			return err
		}
		values := map[string]int{}
		for _, line := range strings.Split(string(out), "\n") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				value, err := strconv.Atoi(parts[1])
				if err == nil {
					values[parts[0]] = value
				}
			}
		}
		x, okX := values["X"]
		y, okY := values["Y"]
		if !okX || !okY {
			return fmt.Errorf("cursor position unavailable")
		}
		path, err := desktop.Movement(desktop.Point{X: x, Y: y}, target, defaultDesktopWidth, defaultDesktopHeight, bend)
		if err != nil {
			return err
		}
		start := time.Now()
		for _, step := range path {
			if delay := time.Until(start.Add(step.At)); delay > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(delay):
				}
			}
			if err = check(); err != nil {
				return err
			}
			if err = run("mousemove", strconv.Itoa(step.X), strconv.Itoa(step.Y)); err != nil {
				return err
			}
		}
		return nil
	}
	button := a.Button
	if button == 0 {
		button = 1
	}
	count := a.Count
	if count == 0 {
		count = 1
	}
	switch a.Action {
	case "type":
		// Small chunks permit takeover checks without placing text in argv.
		runes := []rune(a.Text)
		for len(runes) > 0 {
			if err = check(); err != nil {
				return err
			}
			n := 32
			if len(runes) < n {
				n = len(runes)
			}
			if _, err = desktopInputCommand(ctx, string(runes[:n]), "type", "--clearmodifiers", "--delay", "1", "--file", "-"); err != nil {
				return err
			}
			runes = runes[n:]
		}
	case "key":
		return run("key", "--clearmodifiers", strings.Join(a.Keys, "+"))
	default:
		if err = move(desktop.Point{X: a.X, Y: a.Y}, 0.5); err != nil {
			return err
		}
		if err = check(); err != nil {
			return err
		}
		switch a.Action {
		case "click":
			return desktopClicks(ctx, count, button, check, run)
		case "scroll":
			wheel := map[string]int{"up": 4, "down": 5, "left": 6, "right": 7}[a.Text]
			for i := 0; i < count; i++ {
				if err = check(); err != nil {
					return err
				}
				if err = run("click", strconv.Itoa(wheel)); err != nil {
					return err
				}
			}
		case "drag":
			if err = run("mousedown", strconv.Itoa(button)); err != nil {
				return err
			}
			defer func() {
				release, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_, _ = desktopInputCommand(release, "", "mouseup", strconv.Itoa(button))
			}()
			return move(desktop.Point{X: a.ToX, Y: a.ToY}, 0)
		}
	}
	return nil
}

func DecodeDesktopAction(input []byte) (DesktopAction, error) {
	var action DesktopAction
	if len(input) > 32768 {
		return action, fmt.Errorf("desktop request too large")
	}
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&action); err != nil {
		return action, fmt.Errorf("invalid desktop request")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return action, fmt.Errorf("unexpected trailing input")
	}
	return action, validateDesktopAction(action)
}
