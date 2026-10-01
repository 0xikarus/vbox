package boxruntime

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// The harness MCP process owns this heartbeat. It knows which managed
// conversation it serves and reads that harness's native transcript locally.
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
	sample, err := mascotNativeSample(ctx, home, session, agent)
	text := mascotTranscriptEvidence(sample)
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

// Native transcript roles are specific to box harnesses. Send only agent
// activity text so the controller can classify arbitrary supplied text.
func mascotTranscriptEvidence(sample string) string {
	lines := strings.Split(sample, "\n")
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}
	retained := make([]string, 0, len(lines))
	insideFence := false
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		// User requests provide context, not the agent's current state.
		if strings.HasPrefix(line, "user: ") {
			continue
		}
		line = strings.TrimPrefix(strings.TrimPrefix(line, "assistant: "), "tool: ")
		// Code examples and echoed commands are not status reports.
		if strings.HasPrefix(line, "```") {
			insideFence = !insideFence
			continue
		}
		if insideFence || strings.HasPrefix(line, "$ ") || strings.HasPrefix(line, "> ") {
			continue
		}
		retained = append(retained, line)
	}
	for left, right := 0, len(retained)-1; left < right; left, right = left+1, right-1 {
		retained[left], retained[right] = retained[right], retained[left]
	}
	return strings.Join(retained, "\n")
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
