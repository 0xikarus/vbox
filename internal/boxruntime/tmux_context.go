package boxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type TmuxContext struct {
	Box    string `json:"box"`
	Slot   string `json:"slot"`
	State  string `json:"state"`
	Health string `json:"health"`
}

func tmuxContextPath(root string) string { return filepath.Join(tmuxRoot(root), "context.json") }

func SetTmuxContext(ctx context.Context, root, box, slot, state, health string) error {
	value := TmuxContext{Box: box, Slot: slot, State: state, Health: health}
	if err := writeJSONAtomic(tmuxContextPath(root), value, 0600); err != nil {
		return fmt.Errorf("persist tmux context: %w", err)
	}
	output, err := tmuxOutput(ctx, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		if tmuxServerAbsent(err) {
			return nil
		}
		return err
	}
	for _, session := range strings.Fields(string(output)) {
		if err := ApplyTmuxContext(ctx, root, session); err != nil {
			return err
		}
	}
	return nil
}

func ApplyTmuxContext(ctx context.Context, root, session string) error {
	data, err := os.ReadFile(tmuxContextPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read tmux context: %w", err)
	}
	var value TmuxContext
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode tmux context: %w", err)
	}
	values := map[string]string{
		"VMBOX_NAME": value.Box, "VMBOX_COMPUTE_SLOT": value.Slot,
		"VMBOX_ASSIGNMENT_STATE": value.State, "VMBOX_CONNECTION_HEALTH": value.Health,
	}
	for key, item := range values {
		if _, err := tmuxOutput(ctx, "set-environment", "-t", session, key, item); err != nil {
			return fmt.Errorf("set tmux context %s for %s: %w", key, session, err)
		}
	}
	if _, err := tmuxOutput(ctx, "source-file", "/etc/vmbox/tmux.conf"); err != nil {
		return fmt.Errorf("restore tmux guide: %w", err)
	}
	return nil
}
