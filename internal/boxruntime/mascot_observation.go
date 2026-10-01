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
const mascotKeepaliveInterval = 30 * time.Second

type mascotHeartbeatState struct {
	text   string
	sentAt time.Time
}

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

func sendMascotHeartbeat(ctx context.Context, assignment, home, session, agent string, state mascotHeartbeatState, now time.Time) (mascotHeartbeatState, error) {
	sample, err := mascotNativeSample(ctx, home, session, agent)
	text := mascotTranscriptEvidence(sample)
	if err != nil || text == "" {
		return state, err
	}
	if text == state.text && !state.sentAt.IsZero() && now.Sub(state.sentAt) < mascotKeepaliveInterval {
		return state, nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := desktopAgentAPI(requestCtx, assignment, http.MethodPost, "/v1/agent-desktop/mascot-observation", map[string]string{"session": session, "text": text}, nil); err != nil {
		return state, err
	}
	return mascotHeartbeatState{text: text, sentAt: now}, nil
}

// Native transcript roles are specific to box harnesses. Send only agent
// activity text so the controller can classify arbitrary supplied text.
func mascotTranscriptEvidence(sample string) string {
	type evidenceLine struct {
		text     string
		prose    bool
		tool     bool
		required bool
	}
	var accepted []evidenceLine
	insideFence := false
	for _, raw := range strings.Split(sample, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// User requests provide context, not the agent's current state.
		if strings.HasPrefix(line, "user: ") {
			continue
		}
		if tool, ok := strings.CutPrefix(line, "tool: "); ok {
			accepted = append(accepted, evidenceLine{text: "tool: " + tool, tool: true})
			continue
		}
		if strings.HasPrefix(line, "tool-output: ") {
			continue
		}
		line = strings.TrimPrefix(line, "assistant: ")
		// Code examples and echoed commands are not status reports.
		if strings.HasPrefix(line, "```") {
			insideFence = !insideFence
			continue
		}
		if insideFence || strings.HasPrefix(line, "$ ") || strings.HasPrefix(line, "> ") {
			continue
		}
		accepted = append(accepted, evidenceLine{text: line, prose: true})
	}
	proseNeeded := 3
	for i := len(accepted) - 1; i >= 0 && proseNeeded > 0; i-- {
		if accepted[i].prose {
			accepted[i].required = true
			proseNeeded--
		}
	}
	start := len(accepted) - 80
	if start < 0 {
		start = 0
	}
	retained := make([]evidenceLine, 0, len(accepted)-start+3)
	for i, line := range accepted {
		if i < start && !line.required {
			continue
		}
		if line.tool && len(retained) > 0 && retained[len(retained)-1].tool && retained[len(retained)-1].text == line.text {
			continue
		}
		retained = append(retained, line)
	}
	length := func() int {
		total := 0
		for _, line := range retained {
			total += len(line.text) + 1
		}
		return total
	}
	for length() > mascotSampleBytes {
		removed := false
		for i, line := range retained {
			if !line.required {
				retained = append(retained[:i], retained[i+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			break
		}
	}
	lines := make([]string, 0, len(retained))
	for _, line := range retained {
		lines = append(lines, line.text)
	}
	return strings.Join(lines, "\n")
}

// MascotTranscriptEvidence applies the heartbeat's agent-only line filter.
func MascotTranscriptEvidence(sample string) string {
	return mascotTranscriptEvidence(sample)
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
	var state mascotHeartbeatState
	for {
		state, _ = sendMascotHeartbeat(ctx, assignment, home, session, agent, state, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
