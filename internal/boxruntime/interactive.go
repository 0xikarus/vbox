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
	argv := []string{agent}
	switch agent {
	case "codex", "claude":
	case "shell":
		argv = []string{"/bin/bash", "-l"}
	default:
		return fmt.Errorf("interactive agent must be codex, claude, or shell")
	}
	args := []string{"new-session", "-d", "-s", session, "-c", filepath.Join(filepath.Dir(root), "workspace"), "--"}
	args = append(args, argv...)
	_, err := tmuxOutput(ctx, args...)
	return err
}
