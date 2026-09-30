package boxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MCPActivity contains only tool metadata. Tool arguments, replies, images,
// credentials and message text must never enter the Chat activity log.
type MCPActivity struct {
	ID      string `json:"id"`
	Tool    string `json:"tool"`
	Action  string `json:"action,omitempty"`
	Contact bool   `json:"contact,omitempty"`
	Failed  bool   `json:"failed,omitempty"`
}

func localMCPActivityDir(home string) string {
	return filepath.Join(home, ".local", "share", "vmbox", "mcp-activity")
}

func queueLocalMCPActivity(assignment, tool string, args json.RawMessage, callErr error) error {
	known := false
	for _, candidate := range desktopMCPTools() {
		known = known || candidate["name"] == tool
	}
	if !known {
		return nil
	}
	// Unit tests and standalone runtimes have no scoped controller credential.
	if _, err := readDesktopAgentConfig(assignment); err != nil {
		return nil
	}
	var selected struct {
		Contact string `json:"contact"`
		Action  string `json:"action"`
		ReplyTo string `json:"replyTo"`
	}
	_ = json.Unmarshal(args, &selected)
	if tool == "chat_message" && strings.TrimSpace(selected.Contact) == "" && strings.TrimSpace(selected.ReplyTo) != "" {
		return nil // The direct reply already appears in this box's Chat.
	}
	if tool != "heartbeat" {
		selected.Action = ""
	}
	id, err := chatEventID()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	activity := MCPActivity{ID: id, Tool: tool, Action: selected.Action, Contact: selected.Contact != "", Failed: callErr != nil}
	data, err := json.Marshal(activity)
	if err != nil {
		return err
	}
	return writeTextAtomic(filepath.Join(localMCPActivityDir(home), id+".json"), string(data), 0600)
}

// The already-running box-local MCP HTTP façade retries queued activity.
// Hibernation stops this loop; the private queue survives on the box volume.
func runLocalMCPActivity(ctx context.Context, assignment, home string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		_ = drainLocalMCPActivity(ctx, assignment, home)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func drainLocalMCPActivity(ctx context.Context, assignment, home string) error {
	directory := localMCPActivityDir(home)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, leftErr := entries[i].Info()
		right, rightErr := entries[j].Info()
		if leftErr != nil || rightErr != nil {
			return entries[i].Name() < entries[j].Name()
		}
		return left.ModTime().Before(right.ModTime())
	})
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var activity MCPActivity
		if err := json.Unmarshal(data, &activity); err != nil || activity.ID+".json" != entry.Name() {
			_ = os.Rename(path, path+".invalid")
			continue
		}
		var result struct {
			Stored bool `json:"stored"`
		}
		requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = desktopAgentAPI(requestCtx, assignment, http.MethodPost, "/v1/agent-desktop/tool-activity", activity, &result)
		cancel()
		if err != nil || !result.Stored {
			return errors.New("MCP activity delivery unconfirmed")
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
