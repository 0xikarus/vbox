package boxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/0xikarus/vmbox-service/internal/browser"
	"golang.org/x/sys/unix"
)

// DesktopPassword is a controller-to-worker payload, never an MCP argument.
// Transport it only through stdin; callers must not log it or its parse errors.
type DesktopPassword struct {
	Origin string `json:"origin"`
	Value  string `json:"value"`
}

func DecodeDesktopPassword(data []byte) (DesktopPassword, error) {
	var request DesktopPassword
	invalid := fmt.Errorf("invalid private password request")
	if len(data) > 32768 || !utf8.Valid(data) {
		return request, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		return DesktopPassword{}, invalid
	}
	origin, err := browser.Origin(request.Origin)
	if err != nil || origin != request.Origin || len(request.Value) == 0 || len(request.Value) > 4096 {
		return DesktopPassword{}, invalid
	}
	return request, nil
}

// WithDesktopPasswordTarget holds the same input lock as mouse/keyboard actions.
// The callback can resolve a reference from the verified origin before insertion.
// Its final check must be called immediately before the browser mutation.
func WithDesktopPasswordTarget(ctx context.Context, assignment string, fn func(*browser.Client, browser.PasswordTarget, func() error) error) error {
	return withDesktopBrowser(ctx, assignment, func(client *browser.Client, check func() error) error {
		target, err := client.FocusedPassword(ctx)
		if err != nil {
			return err
		}
		if err = check(); err != nil {
			return err
		}
		return fn(client, target, check)
	})
}

func withDesktopBrowser(ctx context.Context, assignment string, fn func(*browser.Client, func() error) error) error {
	if _, err := NativeSessions(ctx, assignment); err != nil {
		return err
	}
	dir := filepath.Dir(desktopSocket(assignment))
	lock, err := os.OpenFile(filepath.Join(dir, "input.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("desktop input unavailable")
	}
	defer lock.Close()
	for {
		err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return fmt.Errorf("desktop input lock unavailable")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(dir, "input-paused")); !os.IsNotExist(err) {
			return fmt.Errorf("desktop input paused or unavailable")
		}
		_, err := NativeSessions(ctx, assignment)
		return err
	}
	if err = check(); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("browser profile unavailable")
	}
	client, err := browser.Connect(ctx, filepath.Join(home, ".config", "vmbox", "chromium"))
	if err != nil {
		return err
	}
	defer client.Close()
	return fn(client, check)
}

func TypeDesktopPassword(ctx context.Context, assignment string, request DesktopPassword) error {
	value := []byte(request.Value)
	request.Value = ""
	defer clear(value)
	return WithDesktopPasswordTarget(ctx, assignment, func(client *browser.Client, target browser.PasswordTarget, check func() error) error {
		if target.Origin != request.Origin {
			return fmt.Errorf("password destination is not authorized")
		}
		if err := check(); err != nil {
			return err
		}
		return client.InsertPassword(ctx, target, request.Origin, value)
	})
}

func DesktopPasswordOrigin(ctx context.Context, assignment string, output io.Writer) error {
	return WithDesktopPasswordTarget(ctx, assignment, func(_ *browser.Client, target browser.PasswordTarget, check func() error) error {
		if err := check(); err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(map[string]string{"origin": target.Origin})
	})
}

func ImportDesktopBrowserState(ctx context.Context, assignment string, data []byte) error {
	state, err := browser.DecodeStateImport(data)
	if err != nil {
		return err
	}
	return withDesktopBrowser(ctx, assignment, func(client *browser.Client, check func() error) error {
		for _, origin := range state.Origins {
			if err := check(); err != nil {
				return err
			}
			if err := client.ApplyState(ctx, browser.StateImport{Version: 1, Origins: []browser.OriginState{origin}}); err != nil {
				return err
			}
		}
		return check()
	})
}
