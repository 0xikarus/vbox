package boxruntime

import (
	"context"
	"fmt"
	"path/filepath"
)

func StartInteractive(ctx context.Context, root, session, agent string) error {
	return StartInteractiveCommand(ctx, root, session, agent, "")
}

func StartInteractiveCommand(ctx context.Context, root, session, agent, startCLI string) error {
	if !processID.MatchString(session) {
		return fmt.Errorf("invalid session name")
	}
	argv, err := interactiveArgv(agent)
	if err != nil {
		return err
	}
	if startCLI != "" {
		if agent != "shell" || len(startCLI) > 16384 {
			return fmt.Errorf("start-cli requires shell and a command up to 16 KiB")
		}
		argv = append(argv, "--start-cli", startCLI)
	}
	args := []string{"new-session", "-d", "-s", session, "-c", filepath.Join(filepath.Dir(root), "workspace"), "--"}
	args = append(args, argv...)
	_, err = tmuxOutput(ctx, args...)
	if err == nil && agent == "shell" {
		_, err = tmuxOutput(ctx, "set-option", "-t", "="+session+":", "@vmbox-shell", "1")
	}
	if err == nil {
		err = ApplyTmuxContext(ctx, root, session)
	}
	return err
}

func interactiveArgv(agent string) ([]string, error) {
	switch agent {
	case "codex", "claude":
		return []string{agent}, nil
	case "shell":
		return []string{"vmbox-runtime", "welcome"}, nil
	default:
		return nil, fmt.Errorf("interactive agent must be codex, claude, or shell")
	}
}
