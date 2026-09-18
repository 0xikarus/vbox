package controller

import (
	"context"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
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
	if existing, found, err := s.Store.AgentBoxMessage(ctx, accountID, message.ID); err != nil {
		return err
	} else if found && existing.State == "delivered" {
		return nil
	}
	// The watched terminal is presentation, not a second chat transport. Every
	// harness has the vmbox-desktop MCP server, so only its structured outbox may
	// create Agent chat messages; scraping rendered TUI output races that outbox
	// and exposes ordinary stdout as duplicate chat bubbles.
	for {
		if done, err := s.pullStructuredAgentReply(ctx, prov, assignment.Slot.ServiceID, accountID, task, message); err != nil {
			s.Logger.Warn("structured agent reply unavailable", "task", task.ID, "message", message.ID, "error", err)
		} else if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
