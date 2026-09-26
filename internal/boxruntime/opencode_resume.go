package boxruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const openCodeResumeQuery = `
import json, sqlite3, sys, urllib.parse
path, workspace, saved, selected = sys.argv[1:]
db = sqlite3.connect('file:' + urllib.parse.quote(path) + '?mode=ro', uri=True, timeout=2)
where = 's.directory=? AND s.parent_id IS NULL AND s.time_created<=? AND m.time_created<=?'
args = [workspace, int(saved), int(saved)]
if selected:
    where += ' AND s.id=?'
    args.append(selected)
row = db.execute('SELECT s.id,s.time_created,MAX(m.time_created) FROM session s JOIN message m ON m.session_id=s.id WHERE ' + where + ' GROUP BY s.id ORDER BY MAX(m.time_created) DESC,s.time_created DESC LIMIT 1', args).fetchone()
print(json.dumps({'id':row[0], 'created':row[1], 'updated':row[2]} if row else None))
`

func FindOpenCodeResumeCandidate(root, home, workspace, session string) (*ResumeCandidate, error) {
	if !processID.MatchString(session) || !strings.HasPrefix(session, "opencode-") {
		return nil, fmt.Errorf("invalid managed OpenCode session")
	}
	snapshot, err := savedManagedSession(root, session, "opencode")
	if err != nil || snapshot == nil {
		return nil, err
	}
	saved := savedConversation(snapshot, session)
	if saved.ConversationChecked && saved.ConversationID == "" {
		return nil, nil
	}
	path := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "python3", "-I", "-c", openCodeResumeQuery, path, workspace, fmt.Sprint(snapshot.SavedAt.UnixMilli()), saved.ConversationID).Output()
	if err != nil {
		return nil, fmt.Errorf("inspect saved OpenCode sessions: %w", err)
	}
	var row *struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
		Updated int64  `json:"updated"`
	}
	if err := json.Unmarshal(output, &row); err != nil {
		return nil, fmt.Errorf("decode saved OpenCode session: %w", err)
	}
	if row == nil || !processID.MatchString(row.ID) {
		return nil, nil
	}
	started := time.UnixMilli(row.Created).UTC()
	active := time.UnixMilli(row.Updated).UTC()
	if started.IsZero() || started.After(snapshot.SavedAt) {
		return nil, nil
	}
	if active.After(snapshot.SavedAt) {
		active = snapshot.SavedAt
	}
	if active.Before(started) {
		active = started
	}
	return &ResumeCandidate{SessionID: row.ID, SavedAt: snapshot.SavedAt, StartedAt: started, LastActiveAt: active}, nil
}

func RestoreOpenCodeConversation(ctx context.Context, root, session string, expected ResumeCandidate) error {
	candidate, err := FindOpenCodeResumeCandidate(root, WorkloadHome(), WorkspaceDirectory(), session)
	if err != nil {
		return err
	}
	if candidate == nil || candidate.SessionID != expected.SessionID || !candidate.SavedAt.Equal(expected.SavedAt) {
		return fmt.Errorf("saved OpenCode session changed; refresh Chat")
	}
	marker, err := tmuxCommand(ctx, "", "show-environment", "-t", "="+session, taskAgentEnvironment)
	if err != nil || strings.TrimSpace(string(marker)) != taskAgentEnvironment+"=opencode" {
		return fmt.Errorf("managed OpenCode pane unavailable")
	}
	argv, err := persistentAgentArgv(session, "opencode")
	if err != nil {
		return err
	}
	argv = append(argv, "--session", candidate.SessionID)
	args := append([]string{"respawn-pane", "-k", "-t", "=" + session + ":0.0", "-c", WorkspaceDirectory(), "--"}, argv...)
	if _, err := tmuxCommand(ctx, "", args...); err != nil {
		return fmt.Errorf("resume OpenCode terminal: %w", err)
	}
	if err := waitForAgentReady(ctx, session, "opencode"); err != nil {
		return err
	}
	client, err := openCodeVisibleClient(ctx, WorkloadHome(), session)
	if err != nil {
		return err
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		identity, err := openCodeBridgeHealth(ctx, client, session)
		if err == nil && identity.SessionID == candidate.SessionID {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("restored OpenCode conversation is not visible")
		case <-time.After(200 * time.Millisecond):
		}
	}
}
