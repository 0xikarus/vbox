package boxruntime

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// The managed terminal is the common transcript view across Codex, Claude,
// and OpenCode. Capture only the recent visible/scrollback text; no model tool
// call, agent-specific private transcript format, or extra inference process is
// required. The controller retains only the derived state.
const mascotSampleBytes = 8192

func mascotSessions(ctx context.Context) []string {
	listed, err := tmuxCommand(ctx, "", "list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil
	}
	sessions := make([]string, 0, 4)
	for _, session := range strings.Fields(string(listed)) {
		if len(sessions) == 8 {
			break
		}
		if validateTmuxToken("session", session) != nil || strings.HasPrefix(session, "vmbox-internal-") {
			continue
		}
		marker, err := tmuxCommand(ctx, "", "show-environment", "-t", "="+session, taskAgentEnvironment)
		if err != nil {
			continue
		}
		agent := strings.TrimPrefix(strings.TrimSpace(string(marker)), taskAgentEnvironment+"=")
		if agent == "codex" || agent == "claude" || agent == "opencode" {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

func mascotSample(ctx context.Context, session string) (string, error) {
	output, err := tmuxCommand(ctx, "", "capture-pane", "-p", "-J", "-S", "-100", "-t", "="+session+":0.0")
	if err != nil {
		return "", err
	}
	// Keep the newest text, at a UTF-8 boundary. Empty and huge terminal output
	// never causes a large request or allocation in the controller.
	if len(output) > mascotSampleBytes {
		output = output[len(output)-mascotSampleBytes:]
		for len(output) > 0 && output[0]&0xc0 == 0x80 {
			output = output[1:]
		}
	}
	return strings.TrimSpace(string(output)), nil
}

func sendMascotHeartbeat(ctx context.Context, assignment string) {
	for _, session := range mascotSessions(ctx) {
		if ctx.Err() != nil {
			return
		}
		text, err := mascotSample(ctx, session)
		if err != nil || text == "" {
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = desktopAgentAPI(requestCtx, assignment, http.MethodPost, "/v1/agent-desktop/mascot-observation", map[string]string{"session": session, "text": text}, nil)
		cancel()
	}
}

func runMascotHeartbeat(ctx context.Context, assignment string) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		sendMascotHeartbeat(ctx, assignment)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
