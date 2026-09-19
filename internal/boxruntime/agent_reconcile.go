package boxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func managedAgent(value string) bool {
	switch strings.ToLower(filepath.Base(value)) {
	case "codex", "claude", "opencode":
		return true
	default:
		return false
	}
}

func managedAgentSessionName(name string) bool {
	return strings.HasPrefix(name, codexAppServerPrefix)
}

func managedSnapshotSession(session TmuxSession) bool {
	if managedAgentSessionName(session.Name) {
		return true
	}
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if managedAgent(pane.CurrentCommand) || strings.HasSuffix(pane.ResumeStrategy, "-fresh-conversation") && managedAgent(strings.TrimSuffix(pane.ResumeStrategy, "-fresh-conversation")) {
				return true
			}
		}
	}
	return false
}

func filterManagedAgentSessions(snapshot TmuxSnapshot) (TmuxSnapshot, int) {
	kept := make([]TmuxSession, 0, len(snapshot.Sessions))
	removed := 0
	for _, session := range snapshot.Sessions {
		if managedSnapshotSession(session) {
			removed++
			continue
		}
		kept = append(kept, session)
	}
	snapshot.Sessions = kept
	return snapshot, removed
}

func reconcileSavedAgentSessions(root string) error {
	path := TmuxSnapshotPath(root)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var snapshot TmuxSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode tmux snapshot: %w", err)
	}
	filtered, removed := filterManagedAgentSessions(snapshot)
	if removed == 0 {
		return nil
	}
	filtered.SavedAt = time.Now().UTC()
	return writeJSONAtomic(path, filtered, 0600)
}

// ReconcileManagedAgentSessions invalidates every live and snapshotted agent
// conversation after a profile change. Shell and desktop sessions survive,
// while the next chat starts exactly the newly selected harness and credentials.
func ReconcileManagedAgentSessions(ctx context.Context, root, selectedAgent string) error {
	if selectedAgent != "shell" && !managedAgent(selectedAgent) {
		return fmt.Errorf("invalid selected agent %q", selectedAgent)
	}
	format := "#{session_name}\t#{pane_current_command}"
	output, err := tmuxOutput(ctx, "list-panes", "-a", "-F", format)
	if err != nil {
		if tmuxServerAbsent(err) || strings.HasSuffix(err.Error(), ": no current target") {
			return reconcileSavedAgentSessions(root)
		}
		return err
	}
	sessions := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 || fields[0] == "" {
			continue
		}
		if managedAgent(fields[1]) {
			sessions[fields[0]] = true
		}
	}
	list, _ := tmuxOutput(ctx, "list-sessions", "-F", "#{session_name}")
	for _, session := range strings.Fields(string(list)) {
		// Codex uses a companion app-server tmux session whose pane command is
		// node/codex rather than the interactive Codex command and which has no
		// task-agent environment marker. It belongs to the named conversation
		// and must not survive credential or harness reconciliation.
		if managedAgentSessionName(session) {
			sessions[session] = true
			continue
		}
		marker, markerErr := tmuxOutput(ctx, "show-environment", "-t", session, taskAgentEnvironment)
		if markerErr == nil {
			value := strings.TrimPrefix(strings.TrimSpace(string(marker)), taskAgentEnvironment+"=")
			if managedAgent(value) {
				sessions[session] = true
			}
		}
	}
	names := make([]string, 0, len(sessions))
	for session := range sessions {
		names = append(names, session)
	}
	sort.Strings(names)
	for _, session := range names {
		if _, err := tmuxOutput(ctx, "kill-session", "-t", "="+session); err != nil && !tmuxServerAbsent(err) {
			return fmt.Errorf("stop stale agent session %s: %w", session, err)
		}
	}
	_, err = SaveTmuxState(ctx, root)
	return err
}
