package cli

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/transport"
)

func (a *App) controllerDesktop(ctx context.Context, c config.Context, token string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vmbox desktop BOX [--enable] [--no-viewer] [--viewer PATH]")
	}
	flags := flag.NewFlagSet("desktop", flag.ContinueOnError)
	flags.SetOutput(a.Err)
	enable := flags.Bool("enable", false, "install optional desktop packages on this worker")
	noViewer := flags.Bool("no-viewer", false, "print local VNC address and wait for a viewer")
	viewer := flags.String("viewer", "vncviewer", "TigerVNC-compatible viewer executable")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected desktop arguments")
	}
	// Fail locally before waking compute when the requested viewer is missing.
	var viewerPath string
	if !*noViewer {
		var err error
		viewerPath, err = exec.LookPath(*viewer)
		if err != nil {
			return fmt.Errorf("VNC viewer not found; install TigerVNC Viewer or use vmbox desktop %s --no-viewer", tuiLabel(args[0], 100))
		}
	}
	if err := a.requireCapability(ctx, c, token, "nativeAttach"); err != nil {
		return err
	}
	box, err := a.controllerLogicalBox(ctx, c, token, args[0])
	if err != nil {
		return err
	}
	bp := "/v1/logical-boxes/" + url.PathEscape(box.ID)
	if box.State != v1.LogicalBoxRunning {
		var allocation v1.Allocation
		_, err = a.request(ctx, c, token, http.MethodPost, bp+"/allocate", map[string]string{"leaseOwner": "cli-desktop"}, &allocation, map[string]string{"Idempotency-Key": fmt.Sprintf("cli-desktop:%s:%d", box.ID, time.Now().UnixNano())})
		if err != nil {
			return err
		}
		waitCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
		_, err = a.waitAllocation(waitCtx, c, token, allocation)
		cancel()
		if err != nil {
			return fmt.Errorf("desktop resume observation ended; the controller may still be working; check vmbox status %s: %w", tuiLabel(box.Name, 100), err)
		}
	}
	if *enable {
		fmt.Fprintln(a.Err, "Installing desktop packages on this worker…")
		// Package enablement has a three-minute server bound, longer than ordinary API calls.
		client := *a.HTTP
		client.Timeout = 190 * time.Second
		long := *a
		long.HTTP = &client
		_, err := long.request(ctx, c, token, http.MethodPost, bp+"/desktop/enable", map[string]any{}, nil, nil)
		a.authReplacements = long.authReplacements
		if err != nil {
			return fmt.Errorf("desktop enablement failed (check state before retrying): %w", err)
		}
	}
	if _, err := a.request(ctx, c, token, http.MethodPost, bp+"/desktop", map[string]any{}, nil, nil); err != nil {
		return fmt.Errorf("desktop startup failed: %w; if packages are missing, use vmbox desktop %s --enable", err, tuiLabel(box.Name, 100))
	}
	inv, err := a.sessionInventory(ctx, c, token, box.ID)
	if err != nil {
		return err
	}
	var session *v1.Session
	for i := range inv.Sessions {
		if inv.Sessions[i].Name == "vmbox-desktop" {
			session = &inv.Sessions[i]
		}
	}
	if session == nil || inv.State != "live" {
		return fmt.Errorf("desktop session is not live; reconnect to inspect current state")
	}
	var conn v1.NativeConnection
	query := url.Values{"sessionId": {session.ID}, "incarnation": {session.Incarnation}}
	if _, err := a.request(ctx, c, token, http.MethodGet, bp+"/native-connection?"+query.Encode(), nil, &conn, nil); err != nil {
		return err
	}
	if conn.Assignment != inv.Assignment || conn.SessionID != session.ID || conn.Incarnation != session.Incarnation || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(conn.Assignment) {
		return fmt.Errorf("desktop assignment changed; reconnect")
	}
	var view func(context.Context, string) error
	if !*noViewer {
		view = func(ctx context.Context, address string) error {
			host, port, _ := net.SplitHostPort(address)
			runner := a.Runner
			if runner == nil {
				runner = procexec.OSRunner{}
			}
			result, err := runner.Run(ctx, []string{viewerPath, host + "::" + port}, nil, a.Err, a.Err)
			if err != nil {
				return err
			}
			if result.ExitCode != 0 {
				return fmt.Errorf("desktop viewer exited %d; the box was left running", result.ExitCode)
			}
			return nil
		}
	}
	return transport.DesktopTunnel(ctx, func(ctx context.Context, file *os.File) error {
		result, err := a.resolvedExec(ctx, c, token, conn.Connection, []string{"vmbox-runtime", "desktop-stream", conn.Assignment}, provider.ExecOptions{Stdin: file, Stdout: file, Stderr: a.Err}, true)
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("desktop connection exited %d; reconnect with vmbox desktop %s", result.ExitCode, tuiLabel(box.Name, 100))
		}
		return nil
	}, view, func(address string) {
		if *noViewer {
			fmt.Fprintf(a.Out, "vnc://%s\n", address)
			fmt.Fprintln(a.Err, "Private local tunnel; connect your viewer here. Ctrl-C closes it, not the box.")
		} else {
			fmt.Fprintf(a.Err, "Desktop: %s. Closing the viewer leaves the box running.\n", tuiLabel(box.Name, 100))
		}
	})
}
