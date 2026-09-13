package boxruntime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DesktopIdleSeconds measures input and terminal activity, never viewer polls.
// Unknown foreground jobs and human takeover fail closed.
func DesktopIdleSeconds(ctx context.Context, assignment string) (int64, error) {
	inv, err := NativeSessions(ctx, assignment)
	if err != nil {
		return 0, err
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(desktopSocket(assignment)), "input-paused")); err == nil {
		return 0, nil
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, "xprintidle")
	cmd.Env = append(os.Environ(), "DISPLAY=:99")
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("desktop idle observation unavailable")
	}
	milliseconds, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil || milliseconds < 0 {
		return 0, fmt.Errorf("invalid desktop idle observation")
	}
	idle := milliseconds / 1000
	for _, session := range inv.Sessions {
		if session.Name == "vmbox-desktop" || strings.HasPrefix(session.Name, "vmbox-internal-") {
			continue
		}
		output, err := tmuxOutput(ctx, "list-panes", "-s", "-t", session.ID, "-F", "#{pane_current_command}\t#{session_activity}")
		if err != nil {
			return 0, err
		}
		marker, _ := tmuxOutput(ctx, "show-environment", "-t", session.ID, taskAgentEnvironment)
		agent := strings.TrimPrefix(strings.TrimSpace(string(marker)), taskAgentEnvironment+"=")
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) != 2 {
				return 0, fmt.Errorf("unknown desktop foreground activity")
			}
			command := fields[0]
			known := command == "bash" || command == "sh" || command == "zsh" || command == "fish"
			if agent == "codex" || agent == "claude" || agent == "opencode" {
				known = known || command == agent || command == "node" || command == "bun"
			}
			if !known {
				return 0, nil
			}
			at, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0, err
			}
			elapsed := time.Now().Unix() - at
			if elapsed < 0 {
				elapsed = 0
			}
			if elapsed < idle {
				idle = elapsed
			}
		}
	}
	if _, err = NativeSessions(ctx, assignment); err != nil {
		return 0, err
	}
	return idle, nil
}
