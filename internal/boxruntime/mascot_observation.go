package boxruntime

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// The harness MCP process owns this heartbeat. It already knows which managed
// conversation it serves, and reads that harness's native transcript locally.
// Neither the model nor a terminal screen capture is involved.
const mascotSampleBytes = 8192

func mascotClientAgent(name string) string {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "codex"):
		return "codex"
	case strings.Contains(name, "claude"):
		return "claude"
	case strings.Contains(name, "opencode"):
		return "opencode"
	default:
		return ""
	}
}

func sendMascotHeartbeat(ctx context.Context, assignment, home, session, agent, previous string) (string, error) {
	text, err := mascotNativeSample(ctx, home, session, agent)
	if err != nil || text == "" {
		return previous, err
	}
	if text == previous {
		return previous, nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := desktopAgentAPI(requestCtx, assignment, http.MethodPost, "/v1/agent-desktop/mascot-observation", map[string]string{"session": session, "text": text}, nil); err != nil {
		return previous, err
	}
	return text, nil
}

// MCP clients may scrub the child's environment. Codex's app server still
// carries the managed session binding, so its MCP child can read that single
// variable from an ancestor process. No tmux query or screen capture is made.
func mascotMCPSession(agent string) (string, error) {
	if value := os.Getenv("VMBOX_CHAT_SESSION"); validateTmuxToken("session", value) == nil {
		return value, nil
	}
	if agent == "codex" {
		pid := os.Getppid()
		for depth := 0; pid > 1 && depth < 32; depth++ {
			data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
			if err == nil {
				for _, entry := range strings.Split(string(data), "\x00") {
					if value, ok := strings.CutPrefix(entry, "VMBOX_CHAT_SESSION="); ok && validateTmuxToken("session", value) == nil {
						return value, nil
					}
				}
			}
			next, err := parentProcessID(pid)
			if err != nil || next <= 1 || next == pid {
				break
			}
			pid = next
		}
	}
	return "", fmt.Errorf("managed MCP conversation binding unavailable")
}

func runMascotHeartbeat(ctx context.Context, assignment, agent string) {
	session, err := mascotMCPSession(agent)
	if err != nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var previous string
	for {
		previous, _ = sendMascotHeartbeat(ctx, assignment, home, session, agent, previous)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
