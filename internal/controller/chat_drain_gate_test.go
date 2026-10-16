package controller

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type gatedChatProvider struct {
	fakeProvider
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
	fail    bool
}

func (p *gatedChatProvider) Exec(ctx context.Context, _ string, _ []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	p.calls.Add(1)
	if p.entered != nil {
		p.entered <- struct{}{}
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return provider.ExecResult{}, ctx.Err()
		}
	}
	if p.fail {
		return provider.ExecResult{}, errors.New("worker unavailable")
	}
	return provider.ExecResult{}, nil
}

func TestChatOutboxReadersShareOneInFlightDrain(t *testing.T) {
	s := &Server{}
	p := &gatedChatProvider{entered: make(chan struct{}, 32), release: make(chan struct{})}
	task := v1.BoxTask{ID: "task", Agent: "codex"}
	first := make(chan error, 1)
	go func() {
		_, err := s.pullStructuredAgentReply(t.Context(), p, "service", "account", task, v1.BoxMessage{ID: "first"})
		first <- err
	}()
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("first poll never entered")
	}
	// Even a drain older than the cleanup horizon must remain exclusive.
	s.mu.Lock()
	state := s.chatDrains["account:task"]
	state.finished = time.Now().Add(-2 * time.Minute)
	s.chatDrains["account:task"] = state
	s.mu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.pullStructuredAgentReply(t.Context(), p, "service", "account", task, v1.BoxMessage{ID: "another"}); err != nil {
				t.Error(err)
			}
		}()
	}
	// Browser reads, ready notifications and reconciliation use drainAgentChat;
	// a nil Store makes any accidental second drain fail the test immediately.
	if err := s.drainAgentChat(t.Context(), "account", task); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if p.calls.Load() != 1 {
		t.Fatalf("polls while blocked=%d", p.calls.Load())
	}
	close(p.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if _, err := s.pullStructuredAgentReply(t.Context(), p, "service", "account", task, v1.BoxMessage{}); err != nil {
		t.Fatal(err)
	}
	if p.calls.Load() != 1 {
		t.Fatal("completion did not start a cooldown")
	}
	// Another account or task must not be held up by this outbox.
	for _, key := range []string{"other:task", "account:other"} {
		if !s.claimChatDrain(key) {
			t.Fatalf("unrelated drain blocked: %s", key)
		}
		s.finishChatDrain(key)
	}
	s.mu.Lock()
	s.chatDrains["account:task"] = chatDrainState{finished: time.Now().Add(-chatDrainInterval)}
	s.mu.Unlock()
	if _, err := s.pullStructuredAgentReply(t.Context(), p, "service", "account", task, v1.BoxMessage{}); err != nil {
		t.Fatal(err)
	}
	if p.calls.Load() != 2 {
		t.Fatal("poll did not resume after cooldown")
	}
}

func TestChatOutboxFailureReleasesGateWithCooldown(t *testing.T) {
	s := &Server{}
	p := &gatedChatProvider{fail: true}
	task := v1.BoxTask{ID: "task", Agent: "claude"}
	if _, err := s.pullStructuredAgentReply(t.Context(), p, "service", "account", task, v1.BoxMessage{}); err == nil {
		t.Fatal("missing transport error")
	}
	if s.chatDrains["account:task"].running {
		t.Fatal("failed poll left the gate locked")
	}
	if s.claimChatDrain("account:task") {
		t.Fatal("failure triggered immediate retry storm")
	}
}
