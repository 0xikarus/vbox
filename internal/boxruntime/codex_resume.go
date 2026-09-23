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

var codexRolloutID = regexp.MustCompile(`^rollout-.*-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.jsonl$`)

// CodexResumeCandidate is scoped to a hibernation snapshot, never to a later
// empty conversation created when the box wakes.
type CodexResumeCandidate struct {
	SessionID    string    `json:"sessionId"`
	SavedAt      time.Time `json:"savedAt"`
	StartedAt    time.Time `json:"startedAt"`
	LastActiveAt time.Time `json:"lastActiveAt"`
}

func FindCodexResumeCandidate(root, home, session string) (*CodexResumeCandidate, error) {
	if !processID.MatchString(session) || !strings.HasPrefix(session, "codex-") {
		return nil, fmt.Errorf("invalid managed Codex session")
	}
	data, err := os.ReadFile(TmuxSnapshotPath(root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshot TmuxSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode hibernation snapshot: %w", err)
	}
	if snapshot.SavedAt.IsZero() {
		return nil, nil
	}
	found := false
	for _, saved := range snapshot.Sessions {
		if saved.Name == session && saved.ManagedAgent == "codex" {
			found = true
			break
		}
	}
	if !found {
		return nil, nil
	}
	paths, err := filepath.Glob(filepath.Join(home, ".codex", "sessions", "*", "*", "*", "rollout-*.jsonl"))
	if err != nil {
		return nil, err
	}
	var newest *CodexResumeCandidate
	for _, path := range paths {
		match := codexRolloutID.FindStringSubmatch(filepath.Base(path))
		if match == nil {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		info, infoErr := file.Stat()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 2<<20)
		var meta struct {
			Type    string `json:"type"`
			Payload struct {
				ID             string    `json:"id"`
				Timestamp      time.Time `json:"timestamp"`
				Source         string    `json:"source"`
				ParentThreadID string    `json:"parent_thread_id"`
			} `json:"payload"`
		}
		valid := scanner.Scan() && json.Unmarshal(scanner.Bytes(), &meta) == nil
		_ = file.Close()
		// A local TUI records source=cli; the managed --remote TUI records
		// source=vscode in Codex 0.155. Both are interactive conversations.
		if !valid || infoErr != nil || meta.Type != "session_meta" || meta.Payload.ID != match[1] || (meta.Payload.Source != "cli" && meta.Payload.Source != "vscode") || meta.Payload.ParentThreadID != "" {
			continue
		}
		started := meta.Payload.Timestamp
		if started.IsZero() || started.After(snapshot.SavedAt) {
			continue
		}
		// Rollout modification time follows conversation activity. Cap it at
		// the hibernation snapshot: Codex may flush a final record as tmux exits.
		active := info.ModTime()
		if active.After(snapshot.SavedAt) {
			active = snapshot.SavedAt
		}
		if active.Before(started) {
			active = started
		}
		if newest == nil || active.After(newest.LastActiveAt) || (active.Equal(newest.LastActiveAt) && started.After(newest.StartedAt)) {
			newest = &CodexResumeCandidate{SessionID: match[1], SavedAt: snapshot.SavedAt, StartedAt: started, LastActiveAt: active}
		}
	}
	return newest, nil
}

// RestoreCodexConversation replaces only the selected managed pane, after an
// owner explicitly chooses the saved session. The app server stays attached.
func RestoreCodexConversation(ctx context.Context, root, session string, expected CodexResumeCandidate) error {
	candidate, err := FindCodexResumeCandidate(root, WorkloadHome(), session)
	if err != nil {
		return err
	}
	if candidate == nil || candidate.SessionID != expected.SessionID || !candidate.SavedAt.Equal(expected.SavedAt) {
		return fmt.Errorf("saved Codex session changed; refresh Chat")
	}
	marker, err := tmuxCommand(ctx, "", "show-environment", "-t", "="+session, taskAgentEnvironment)
	if err != nil || strings.TrimSpace(string(marker)) != taskAgentEnvironment+"=codex" {
		return fmt.Errorf("managed Codex pane unavailable")
	}
	client, err := dialCodexAppServer(ctx, session)
	if err != nil {
		return err
	}
	_, err = client.call(ctx, "thread/resume", map[string]any{"threadId": candidate.SessionID})
	client.Close()
	if err != nil {
		return fmt.Errorf("saved Codex thread unavailable: %w", err)
	}
	argv := codexRemoteResumeArgv(session, candidate.SessionID)
	if _, err := tmuxCommand(ctx, "", "respawn-pane", "-k", "-t", "="+session+":0.0", "-c", WorkspaceDirectory(), shellJoin(argv)); err != nil {
		return fmt.Errorf("resume Codex terminal: %w", err)
	}
	if err := waitForAgentReady(ctx, session, "codex"); err != nil {
		return fmt.Errorf("restored Codex terminal did not become ready: %w", err)
	}
	if err := rememberCodexThread(root, session, candidate.SessionID); err != nil {
		return err
	}
	return writeTextAtomic(codexResetPendingFile(root, session), candidate.SessionID+"\n", 0600)
}

func codexRemoteResumeArgv(session, threadID string) []string {
	return []string{"codex", "resume", "--remote", codexAppServerURL(session),
		"-c", "check_for_update_on_startup=false", "-c", "suppress_unstable_features_warning=true",
		"-c", "notice.hide_rate_limit_model_nudge=true", threadID}
}
