package boxruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// visibleManagedConversation records the TUI's selected native conversation
// while it is still alive. Older snapshots without this field can use the
// agent's saved history as a fallback.
func visibleManagedConversation(ctx context.Context, agent, session string) (string, bool) {
	switch agent {
	case "codex":
		connected, id, err := codexVisibleThreadState(ctx, session)
		if err != nil || !connected || id != "" && !processID.MatchString(id) {
			return "", false
		}
		return id, true
	case "opencode":
		client, err := openCodeVisibleClient(ctx, WorkloadHome(), session)
		if err != nil {
			return "", false
		}
		identity, err := openCodeBridgeHealth(ctx, client, session)
		if err != nil || identity.SessionID != "" && !processID.MatchString(identity.SessionID) {
			return "", false
		}
		return identity.SessionID, true
	case "claude":
		entries, err := os.ReadDir(claudeChannelReadyDir(WorkloadHome()))
		if err != nil {
			return "", false
		}
		var selected string
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), session+".channel_") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(claudeChannelReadyDir(WorkloadHome()), entry.Name()))
			if err != nil {
				continue
			}
			var marker struct {
				Owner     string `json:"owner"`
				PID       int    `json:"pid"`
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(data, &marker) != nil || marker.Owner != strings.TrimPrefix(entry.Name(), session+".") || !claudeChannelOwnerAlive(marker.PID) || !claudeSessionID.MatchString(marker.SessionID) {
				continue
			}
			if selected != "" && selected != marker.SessionID {
				return "", false
			}
			selected = marker.SessionID
		}
		return selected, selected != ""
	}
	return "", false
}
