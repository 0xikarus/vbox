package controller

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

const (
	agentReplyPollInterval = 2 * time.Second
	agentReplyWatchTimeout = 10 * time.Minute
)

func (s *Server) watchAgentReply(accountID string, task v1.BoxTask, message v1.BoxMessage) {
	if task.Agent == "shell" {
		return
	}
	key := accountID + ":" + message.ID
	s.mu.Lock()
	if _, exists := s.replyWatches[key]; exists {
		s.mu.Unlock()
		return
	}
	s.replyWatches[key] = struct{}{}
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.replyWatches, key)
			s.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), agentReplyWatchTimeout)
		defer cancel()
		if err := s.captureAgentReply(ctx, accountID, task, message); err != nil && ctx.Err() == nil {
			s.Logger.Warn("agent reply capture stopped", "task", task.ID, "message", message.ID, "error", err)
		}
	}()
}

func (s *Server) captureAgentReply(ctx context.Context, accountID string, task v1.BoxTask, message v1.BoxMessage) error {
	p := taskPrincipal(accountID, task)
	box, err := s.Store.LogicalBox(ctx, p, task.LogicalBoxID)
	if err != nil {
		return err
	}
	assignment, err := s.Store.assignment(ctx, accountID, box.ID)
	if err != nil {
		return err
	}
	prov, err := s.provider(ctx, accountID, box.Provider, box.ProviderCredential)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(agentReplyPollInterval)
	defer ticker.Stop()
	lastReply := ""
	stableCompletePolls := 0
	for {
		result, execErr := prov.Exec(ctx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "tmux-screen", task.Session, "2000"}, provider.ExecOptions{})
		if execErr == nil && result.ExitCode == 0 {
			var snapshot v1.TerminalSnapshot
			if json.Unmarshal([]byte(result.Stdout), &snapshot) == nil {
				reply, complete := extractAgentReply(task.Agent, message.Text, snapshot.Content)
				if reply != "" {
					if reply != lastReply {
						lastReply = reply
						stableCompletePolls = 0
					}
					if complete {
						stableCompletePolls++
					} else {
						stableCompletePolls = 0
					}
					state := "streaming"
					if stableCompletePolls >= 2 {
						state = "delivered"
					}
					if _, err := s.Store.UpsertAgentBoxMessage(ctx, accountID, task.ID, message.ID, reply, state); err != nil {
						return err
					}
					if state == "delivered" {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func extractAgentReply(agent, prompt, content string) (string, bool) {
	prompt = strings.TrimSpace(strings.Split(strings.ReplaceAll(prompt, "\r", ""), "\n")[0])
	if prompt == "" {
		return "", false
	}
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(content, "\r", ""), "\u00a0", " "), "\n")
	start := -1
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if (strings.HasPrefix(line, "›") || strings.HasPrefix(line, "❯")) && strings.TrimSpace(strings.TrimLeft(line, "›❯ ")) == prompt {
			start = index + 1
			break
		}
	}
	if start < 0 {
		return "", false
	}
	end := -1
	for index := start; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		switch agent {
		case "codex":
			if line == "›" || strings.HasPrefix(line, "› Ask Codex") {
				end = index
			}
		case "claude":
			if line == "❯" || strings.HasPrefix(line, "❯ Try \"") {
				end = index
			}
		case "opencode":
			if line == ">" || line == "❯" {
				end = index
			}
		}
		if end >= 0 {
			break
		}
	}
	complete := end >= 0
	if !complete {
		end = len(lines)
	}

	answer := make([]string, 0, end-start)
	seenResponse := false
	for _, raw := range lines[start:end] {
		line := strings.TrimSpace(raw)
		if line == "" {
			if seenResponse && len(answer) > 0 && answer[len(answer)-1] != "" {
				answer = append(answer, "")
			}
			continue
		}
		if strings.HasPrefix(line, "•") || strings.HasPrefix(line, "●") || strings.HasPrefix(line, "⏺") {
			seenResponse = true
			line = strings.TrimSpace(strings.TrimLeft(line, "•●⏺ "))
		}
		if !seenResponse || decorativeAgentLine(line) {
			continue
		}
		answer = append(answer, line)
	}
	for len(answer) > 0 && answer[len(answer)-1] == "" {
		answer = answer[:len(answer)-1]
	}
	reply := strings.TrimSpace(strings.Join(answer, "\n"))
	return reply, complete && reply != ""
}

func decorativeAgentLine(line string) bool {
	return strings.HasPrefix(line, "✻ ") || strings.HasPrefix(line, "✘ Auto-update") ||
		strings.HasPrefix(line, "⏵⏵ ") || strings.HasPrefix(line, "? for shortcuts") ||
		strings.Trim(line, "─━═ ") == "" || strings.Contains(line, " · /data/workspace")
}
