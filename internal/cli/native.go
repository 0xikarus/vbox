package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/transport"
)

func (a *App) nativeTransport() transport.SSH {
	return transport.SSH{Runner: a.Runner, IdentityFile: a.Environ["VMBOX_SSH_IDENTITY_FILE"], KnownHostsFile: a.Environ["VMBOX_SSH_KNOWN_HOSTS_FILE"]}
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func (a *App) requireCapability(ctx context.Context, c config.Context, token, capability string) error {
	var caps map[string]bool
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/capabilities", nil, &caps, nil); err != nil {
		return fmt.Errorf("controller capability check failed; upgrade/configure controller before continuing: %w", err)
	}
	if !caps[capability] {
		return fmt.Errorf("controller does not support or authorize %s", capability)
	}
	return nil
}

func (a *App) sessionInventory(ctx context.Context, c config.Context, token, box string) (v1.SessionInventory, error) {
	var inv v1.SessionInventory
	_, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes/"+url.PathEscape(box)+"/sessions", nil, &inv, nil)
	return inv, err
}

func (a *App) controllerSessions(ctx context.Context, c config.Context, token string, args []string) error {
	if len(args) == 2 && args[1] == "--enable" {
		if err := a.requireCapability(ctx, c, token, "nativeAttach"); err != nil {
			return err
		}
		_, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(args[0])+"/sessions/enable", map[string]any{}, nil, nil)
		return err
	}
	if len(args) < 1 || len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
		return fmt.Errorf("usage: vbox sessions BOX [--json]")
	}
	inv, err := a.sessionInventory(ctx, c, token, args[0])
	if err != nil {
		return err
	}
	if len(args) == 2 {
		return json.NewEncoder(a.Out).Encode(inv)
	}
	fmt.Fprintf(a.Out, "%s (%s; observed %s; partial=%t)\n", args[0], inv.State, inv.ObservedAt, inv.Partial)
	for _, s := range inv.Sessions {
		fmt.Fprintf(a.Out, "%s\t%q\t%d panes\n", s.ID, s.Name, s.Panes)
	}
	return nil
}

func (a *App) attachNative(ctx context.Context, c config.Context, token string, box v1.LogicalBox, name string) error {
	return a.attachRemembered(ctx, c, token, box, name, nil, false)
}

func (a *App) attachRemembered(ctx context.Context, c config.Context, token string, box v1.LogicalBox, name string, expected *v1.Session, remember bool) error {
	if a.IsTerminal == nil || !a.IsTerminal() {
		return fmt.Errorf("attachment requires a terminal; use vbox sessions or task --json")
	}
	inv, err := a.sessionInventory(ctx, c, token, box.ID)
	if err != nil {
		return err
	}
	if inv.State != "live" {
		return fmt.Errorf("session inventory is %s", inv.State)
	}
	if name == "" && len(inv.Sessions) == 0 {
		_, err = a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(box.ID)+"/sessions/welcome", map[string]any{}, &inv, nil)
		if err != nil {
			return err
		}
	}
	var selected *v1.Session
	if name != "" {
		for i := range inv.Sessions {
			if inv.Sessions[i].Name == name {
				selected = &inv.Sessions[i]
				break
			}
		}
	} else if len(inv.Sessions) == 1 {
		selected = &inv.Sessions[0]
	} else {
		for i, s := range inv.Sessions {
			fmt.Fprintf(a.Err, "%d. %q\n", i+1, s.Name)
		}
		fmt.Fprint(a.Err, "Session number: ")
		line, readErr := bufio.NewReader(a.In).ReadString('\n')
		if readErr != nil {
			return readErr
		}
		i, parseErr := strconv.Atoi(strings.TrimSpace(line))
		if parseErr != nil || i < 1 || i > len(inv.Sessions) {
			return fmt.Errorf("invalid session selection")
		}
		selected = &inv.Sessions[i-1]
	}
	if selected == nil {
		return fmt.Errorf("exact session %q not found (partial inventory=%t); nothing created", name, inv.Partial)
	}
	if expected != nil && (selected.ID != expected.ID || selected.Incarnation != expected.Incarnation) {
		return fmt.Errorf("selected session was recreated; select again (nothing attached)")
	}
	var conn v1.NativeConnection
	query := url.Values{"sessionId": {selected.ID}, "incarnation": {selected.Incarnation}}
	if _, err = a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes/"+url.PathEscape(box.ID)+"/native-connection?"+query.Encode(), nil, &conn, nil); err != nil {
		return err
	}
	if conn.LogicalBoxID != box.ID || conn.SessionID != selected.ID || conn.Incarnation != selected.Incarnation || conn.Assignment != inv.Assignment {
		return fmt.Errorf("controller returned a mismatched session handoff")
	}
	if remember {
		if _, err = a.request(ctx, c, token, http.MethodPut, "/v1/logical-boxes/"+url.PathEscape(box.ID)+"/sessions/primary", map[string]string{"sessionId": selected.ID, "incarnation": selected.Incarnation}, nil, nil); err != nil {
			return fmt.Errorf("could not remember primary session: %w", err)
		}
	}
	restore, err := makeRaw(a.In)
	if err != nil {
		return err
	}
	result, err := a.resolvedExec(ctx, c, token, conn.Connection, []string{"vmbox-runtime", "native-attach", conn.Assignment, conn.SessionID, conn.Incarnation}, provider.ExecOptions{Interactive: true, Stdin: a.In, Stdout: a.Out, Stderr: a.Err}, false)
	restore()
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("terminal exited %d; input was not replayed; inspect/reconnect with vbox %s --session %s", result.ExitCode, shellQuote(box.Name), shellQuote(selected.Name))
	}
	return a.postControllerInteractiveExit(ctx, c, token, box)
}
