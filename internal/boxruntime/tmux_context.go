package boxruntime

import (
	"context"
	"fmt"
	"strings"
)

func SetTmuxContext(ctx context.Context, box, slot, state, health string) error {
	output, err := tmuxOutput(ctx, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		if tmuxServerAbsent(err) {
			return nil
		}
		return err
	}
	values := map[string]string{
		"VMBOX_NAME":              box,
		"VMBOX_COMPUTE_SLOT":      slot,
		"VMBOX_ASSIGNMENT_STATE":  state,
		"VMBOX_CONNECTION_HEALTH": health,
	}
	for _, session := range strings.Fields(string(output)) {
		for key, value := range values {
			if _, err := tmuxOutput(ctx, "set-environment", "-t", session, key, value); err != nil {
				return fmt.Errorf("set tmux context %s for %s: %w", key, session, err)
			}
		}
	}
	if _, err := tmuxOutput(ctx, "source-file", "/etc/vmbox/tmux.conf"); err != nil {
		return fmt.Errorf("restore tmux guide: %w", err)
	}
	return nil
}
