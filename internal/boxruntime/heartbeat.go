package boxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type localHeartbeat struct {
	ID              string    `json:"id"`
	Session         string    `json:"session"`
	IntervalMinutes int       `json:"intervalMinutes"`
	TicksLeft       int       `json:"ticksLeft"`
	InitialCount    int       `json:"initialCount"`
	NextAt          time.Time `json:"nextAt"`
}

func localHeartbeatPath(home string) string {
	return filepath.Join(home, ".local", "share", "vmbox", "heartbeat.json")
}

func readLocalHeartbeat(home string) (*localHeartbeat, error) {
	data, err := os.ReadFile(localHeartbeatPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state localHeartbeat
	if json.Unmarshal(data, &state) != nil || validateTmuxToken("heartbeat session", state.Session) != nil || validateTmuxToken("heartbeat ID", state.ID) != nil || state.IntervalMinutes < 5 || state.IntervalMinutes > 1440 || state.TicksLeft < 1 || state.TicksLeft > 1000 || state.InitialCount < state.TicksLeft || state.NextAt.IsZero() {
		return nil, fmt.Errorf("invalid local heartbeat state")
	}
	return &state, nil
}

func writeLocalHeartbeat(home string, state localHeartbeat) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeTextAtomic(localHeartbeatPath(home), string(data), 0600)
}

func startLocalHeartbeat(session string, intervalMinutes, count int) (map[string]any, error) {
	if err := validateTmuxToken("heartbeat session", session); err != nil {
		return nil, err
	}
	if intervalMinutes < 5 || intervalMinutes > 1440 || count < 1 || count > 1000 {
		return nil, fmt.Errorf("intervalMinutes must be 5–1440 and count 1–1000")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	state := localHeartbeat{ID: ID("hb_"), Session: session, IntervalMinutes: intervalMinutes, TicksLeft: count, InitialCount: count, NextAt: time.Now().Add(time.Duration(intervalMinutes) * time.Minute)}
	if err := writeLocalHeartbeat(home, state); err != nil {
		return nil, err
	}
	return map[string]any{"active": true, "intervalMinutes": intervalMinutes, "ticksLeft": count, "nextAt": state.NextAt}, nil
}

func stopLocalHeartbeat() (map[string]any, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	err = os.Remove(localHeartbeatPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{"active": false, "stopped": false}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"active": false, "stopped": true}, nil
}

func heartbeatMessage(at time.Time, ticksLeft int, showRemaining bool) string {
	text := "[Heartbeat] " + at.Local().Format("02.01.2006 15:04:05")
	if showRemaining {
		text += fmt.Sprintf(" [Ticks left:%d]", ticksLeft)
	}
	return text
}

// The HTTP façade is already a box-local process. Its watcher reads a private
// file on the persistent volume, so a TUI restart or hibernation does not erase
// the timer. The controller is consulted only for the existing tool policy.
func runLocalHeartbeats(ctx context.Context, assignment, home, token string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		_ = advanceLocalHeartbeat(ctx, home, time.Now(),
			func(ctx context.Context) (bool, error) {
				allowed, err := desktopAgentToolPolicy(ctx, assignment)
				return allowed["heartbeat"], err
			},
			func(ctx context.Context, state localHeartbeat, text, messageID string) error {
				return postLocalHeartbeat(ctx, assignment, token, state.Session, text, messageID)
			})
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func advanceLocalHeartbeat(ctx context.Context, home string, now time.Time, permitted func(context.Context) (bool, error), deliver func(context.Context, localHeartbeat, string, string) error) error {
	state, err := readLocalHeartbeat(home)
	if err != nil || state == nil || now.Before(state.NextAt) {
		return err
	}
	allowed, err := permitted(ctx)
	if err != nil {
		return err
	}
	if !allowed {
		return os.Remove(localHeartbeatPath(home))
	}
	messageID := state.ID + "_" + strconv.Itoa(state.InitialCount-state.TicksLeft+1)
	message := heartbeatMessage(now, state.TicksLeft-1, state.InitialCount > 1)
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := deliver(requestCtx, *state, message, messageID); err != nil {
		return err
	}
	current, err := readLocalHeartbeat(home)
	if err != nil || current == nil || current.ID != state.ID || current.TicksLeft != state.TicksLeft || !current.NextAt.Equal(state.NextAt) {
		return err
	}
	if current.TicksLeft == 1 {
		return os.Remove(localHeartbeatPath(home))
	}
	current.TicksLeft--
	current.NextAt = time.Now().Add(time.Duration(current.IntervalMinutes) * time.Minute)
	return writeLocalHeartbeat(home, *current)
}

func postLocalHeartbeat(ctx context.Context, assignment, token, session, text, messageID string) error {
	// A restored box may have a new managed tmux session name. Switch only when
	// the old session has disappeared and exactly one managed session exists.
	if _, err := tmuxCommand(ctx, "", "show-environment", "-t", session, taskAgentEnvironment); err != nil {
		current, findErr := soleAgentConversation(ctx)
		if findErr != nil {
			return findErr
		}
		session = current
	}
	data, _ := json.Marshal(map[string]string{"text": text, "session": session, "messageId": messageID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, desktopMCPHTTPURL(assignment)+"/prompt", bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 2 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("local heartbeat prompt returned %d: %s", response.StatusCode, body)
	}
	return nil
}
