package boxruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var claudeSessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// FindClaudeResumeCandidate only considers transcripts from the saved managed
// pane's workspace and activity before that pane's hibernation snapshot.
func FindClaudeResumeCandidate(root, home, workspace, session string) (*ResumeCandidate, error) {
	if !processID.MatchString(session) || !strings.HasPrefix(session, "claude-") {
		return nil, fmt.Errorf("invalid managed Claude session")
	}
	snapshot, err := savedManagedSession(root, session, "claude")
	if err != nil || snapshot == nil {
		return nil, err
	}
	project := "-" + strings.ReplaceAll(strings.TrimPrefix(filepath.Clean(workspace), "/"), "/", "-")
	paths, err := filepath.Glob(filepath.Join(home, ".claude", "projects", project, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	var newest *ResumeCandidate
	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if !claudeSessionID.MatchString(id) {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		info, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			continue
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 4<<20)
		var started time.Time
		for line := 0; line < 128 && scanner.Scan(); line++ {
			var record struct {
				SessionID   string    `json:"sessionId"`
				CWD         string    `json:"cwd"`
				Timestamp   time.Time `json:"timestamp"`
				IsSidechain bool      `json:"isSidechain"`
			}
			if json.Unmarshal(scanner.Bytes(), &record) != nil || record.SessionID != id || filepath.Clean(record.CWD) != workspace || record.IsSidechain || record.Timestamp.IsZero() || record.Timestamp.After(snapshot.SavedAt) {
				continue
			}
			if started.IsZero() || record.Timestamp.Before(started) {
				started = record.Timestamp
			}
			break
		}
		readErr := scanner.Err()
		_ = file.Close()
		if readErr != nil || started.IsZero() {
			continue
		}
		active := info.ModTime()
		if active.After(snapshot.SavedAt) {
			active = snapshot.SavedAt
		}
		if active.Before(started) {
			active = started
		}
		if newest == nil || active.After(newest.LastActiveAt) || active.Equal(newest.LastActiveAt) && started.After(newest.StartedAt) {
			newest = &ResumeCandidate{SessionID: id, SavedAt: snapshot.SavedAt, StartedAt: started, LastActiveAt: active}
		}
	}
	return newest, nil
}

func RestoreClaudeConversation(ctx context.Context, root, session string, expected ResumeCandidate) error {
	candidate, err := FindClaudeResumeCandidate(root, WorkloadHome(), WorkspaceDirectory(), session)
	if err != nil {
		return err
	}
	if candidate == nil || candidate.SessionID != expected.SessionID || !candidate.SavedAt.Equal(expected.SavedAt) {
		return fmt.Errorf("saved Claude session changed; refresh Chat")
	}
	marker, err := tmuxCommand(ctx, "", "show-environment", "-t", "="+session, taskAgentEnvironment)
	if err != nil || strings.TrimSpace(string(marker)) != taskAgentEnvironment+"=claude" {
		return fmt.Errorf("managed Claude pane unavailable")
	}
	if err := EnsureClaudeDefaults(WorkloadHome(), WorkspaceDirectory()); err != nil {
		return err
	}
	priorChannels, err := claudeChannelOwners(WorkloadHome(), session)
	if err != nil {
		return err
	}
	argv, err := persistentAgentArgv(session, "claude")
	if err != nil {
		return err
	}
	argv = append(argv, "--resume", candidate.SessionID)
	args := append([]string{"respawn-pane", "-k", "-t", "=" + session + ":0.0", "-c", WorkspaceDirectory(), "--"}, argv...)
	if _, err := tmuxCommand(ctx, "", args...); err != nil {
		return fmt.Errorf("resume Claude terminal: %w", err)
	}
	if err := waitForAgentReady(ctx, session, "claude"); err != nil {
		return err
	}
	if err := claudeChannelReadyWait(ctx, session, priorChannels); err != nil {
		return err
	}
	return settleAgentReadiness(ctx, session, "claude")
}
