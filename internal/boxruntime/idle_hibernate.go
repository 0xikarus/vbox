package boxruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

// Unlike explicit hibernation, automatic cleanup must never signal arbitrary
// workspace processes or stop a tmux server with a new interactive session.
func PrepareIdleHibernate(ctx context.Context, root string) (HibernateResult, error) {
	result := HibernateResult{StoppedAt: time.Now().UTC()}
	result.SnapshotPath = TmuxSnapshotPath(root)
	// Check and stop in the same tmux command queue. A concurrently added
	// session prevents the stop, and the subsequent probe fails closed.
	// The session loop expands to one x per session, including on tmux 3.3a.
	// server_sessions is unavailable there and expands to an empty string.
	_, err := tmuxOutput(ctx, "if-shell", "-F", "#{==:#{S:x},}", "kill-server", "display-message 'workspace became busy'")
	if err != nil && !tmuxServerAbsent(err) {
		return result, err
	}
	if _, err = tmuxOutput(ctx, "list-sessions"); !tmuxServerAbsent(err) {
		return result, fmt.Errorf("tmux server still present; automatic hibernation refused")
	}
	if _, err = SaveTmuxState(ctx, root); err != nil {
		return result, err
	}
	result.Blockers = workspaceProcesses(filepath.Dir(root), ancestorPIDs())
	if len(result.Blockers) != 0 {
		return result, fmt.Errorf("workspace still has live processes; automatic hibernation refused")
	}
	if err = runSync(ctx); err != nil {
		return result, err
	}
	err = writeJSONAtomic(filepath.Join(root, "hibernate-ready.json"), result, 0600)
	return result, err
}
