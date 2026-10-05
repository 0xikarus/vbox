package sharedworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

// CleanupStaleTemp asks each running workspace to clean its own private /tmp,
// persistent TMPDIR, and old core dumps. The command runs as the box UID,
// inside its namespace when isolation is enabled.
func (s *Store) CleanupStaleTemp(ctx context.Context) (boxruntime.TempCleanupResult, error) {
	runtime, ok := s.Runtime.(*LinuxRuntime)
	if !ok {
		return boxruntime.TempCleanupResult{}, provider.ErrUnsupported
	}
	s.mu.Lock()
	workspaces := make([]Workspace, 0, len(s.state.Slots))
	for _, slot := range s.state.Slots {
		if slot.State != provider.StateRunning || slot.WorkspaceID == "" {
			continue
		}
		if workspace, ok := s.state.Workspaces[slot.WorkspaceID]; ok {
			workspaces = append(workspaces, workspace)
		}
	}
	s.mu.Unlock()
	sort.Slice(workspaces, func(i, j int) bool { return workspaces[i].ID < workspaces[j].ID })
	var total boxruntime.TempCleanupResult
	var problems []error
	for _, workspace := range workspaces {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		boxCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		command, err := runtime.Command(boxCtx, workspace, []string{"vmbox-runtime", "cleanup-temp"})
		if err != nil {
			problems = append(problems, fmt.Errorf("workspace %s cleanup command: %w", workspace.ID, err))
			cancel()
			continue
		}
		output, err := command.Output()
		cancel()
		if err != nil {
			problems = append(problems, fmt.Errorf("workspace %s cleanup: %w", workspace.ID, err))
			continue
		}
		var result boxruntime.TempCleanupResult
		if err := json.Unmarshal(output, &result); err != nil {
			problems = append(problems, fmt.Errorf("workspace %s cleanup result: %w", workspace.ID, err))
			continue
		}
		total.Removed += result.Removed
		total.ReclaimedBytes += result.ReclaimedBytes
		total.Skipped += result.Skipped
	}
	return total, errors.Join(problems...)
}
