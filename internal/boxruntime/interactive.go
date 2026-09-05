package boxruntime

import (
	"context"
	"fmt"
	"path/filepath"
)

func StartInteractive(ctx context.Context, root, session, agent string) error {
	if !processID.MatchString(session) {
		return fmt.Errorf("invalid session name")
	}
	argv, err := interactiveArgv(agent)
	if err != nil {
		return err
	}
	args := []string{"new-session", "-d", "-s", session, "-c", filepath.Join(filepath.Dir(root), "workspace"), "--"}
	args = append(args, argv...)
	_, err = tmuxOutput(ctx, args...)
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

func StartCoworker(ctx context.Context, root string) error {
	_, err := tmuxOutput(ctx, "new-session", "-d", "-s", "coworker-primary", "-c", filepath.Join(filepath.Dir(root), "workspace"), "--", "vmbox-runtime", "coworker-run")
	return err
}
