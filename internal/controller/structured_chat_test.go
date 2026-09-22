package controller

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

// chatDrainProvider replays scripted chat-pull output and records every runtime
// call so a test can assert exactly which outbox events were acknowledged.
type chatDrainProvider struct {
	fakeProvider
	pulls []string
	acks  []string
}

func (p *chatDrainProvider) Exec(_ context.Context, _ string, argv []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	if len(argv) < 2 {
		return provider.ExecResult{}, nil
	}
	switch argv[1] {
	case "chat-pull":
		if len(p.pulls) == 0 {
			return provider.ExecResult{}, nil
		}
		out := p.pulls[0]
		p.pulls = p.pulls[1:]
		return provider.ExecResult{Stdout: out}, nil
	case "chat-ack":
		if len(argv) > 3 {
			p.acks = append(p.acks, argv[3])
		}
		return provider.ExecResult{}, nil
	}
	return provider.ExecResult{}, nil
}

func chatEventJSON(t *testing.T, event boxruntime.ChatEvent) string {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func chatTestServer(store *Store) *Server {
	return &Server{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// TestPullStructuredAgentReplyDrainsExpiredEventBeforeMatchingReply is the
// head-of-line regression: an event whose reference no longer resolves to a
// pending message must be stored on its own and acknowledged, so the reply
// queued behind it is still delivered.
func TestPullStructuredAgentReplyDrainsExpiredEventBeforeMatchingReply(t *testing.T) {
	store, mock := testStore(t)
	prov := &chatDrainProvider{pulls: []string{
		chatEventJSON(t, boxruntime.ChatEvent{ID: "e1", Kind: "reply", ReplyTo: "expired-key", Text: "late note"}),
		chatEventJSON(t, boxruntime.ChatEvent{ID: "e2", Kind: "reply", ReplyTo: "chat-key-1", Text: "answer"}),
	}}
	mock.ExpectQuery("FROM box_messages").WithArgs("account-a", "task-1", "expired-key").
		WillReturnRows(emptyBoxMessageRows())
	mock.ExpectQuery("INSERT INTO box_messages").
		WithArgs(sqlmock.AnyArg(), "account-a", "task-1", "late note", "agent-message:e1").
		WillReturnRows(boxMessageRow("message-late", "task-1", "", "agent", "late note", "delivered"))
	mock.ExpectExec("UPDATE box_tasks SET agent_busy").WithArgs("account-a", "task-1", false).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM box_messages").WithArgs("account-a", "task-1", "chat-key-1").
		WillReturnRows(boxMessageRow("message-1", "task-1", "user-a", "user", "hello", "delivered"))
	mock.ExpectQuery("FROM box_messages").WithArgs("account-a", "agent-reply:message-1").
		WillReturnRows(emptyBoxMessageRows())
	mock.ExpectExec("INSERT INTO box_messages").
		WithArgs(sqlmock.AnyArg(), "account-a", "task-1", "answer", "delivered", "agent-reply:message-1", "message-1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("FROM box_messages").WithArgs("account-a", "agent-reply:message-1").
		WillReturnRows(boxMessageRow("reply-1", "task-1", "", "agent", "answer", "delivered"))
	mock.ExpectExec("UPDATE box_tasks SET agent_busy=false").WithArgs("account-a", "task-1", "message-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	server := chatTestServer(store)
	task := v1.BoxTask{ID: "task-1", LogicalBoxID: "box-1", Agent: "claude", Session: "claude-1"}
	request := v1.BoxMessage{ID: "message-1", TaskID: "task-1"}
	done, err := server.pullStructuredAgentReply(context.Background(), prov, "service-1", "account-a", task, request)
	if err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if len(prov.acks) != 2 || prov.acks[0] != "e1" || prov.acks[1] != "e2" {
		t.Fatalf("both events must be acknowledged in order, got %v", prov.acks)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestApplyChatEventKeepsAnsweredReplyAsOwnMessage guards the fallback for a
// repeat delivery: the original message already has a reply, so the new text
// becomes a standalone agent message instead of overwriting the answer.
func TestApplyChatEventKeepsAnsweredReplyAsOwnMessage(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("FROM box_messages").WithArgs("account-a", "task-1", "chat-key-1").
		WillReturnRows(boxMessageRow("message-1", "task-1", "user-a", "user", "hello", "delivered"))
	mock.ExpectQuery("FROM box_messages").WithArgs("account-a", "agent-reply:message-1").
		WillReturnRows(boxMessageRow("reply-1", "task-1", "", "agent", "answer", "delivered"))
	mock.ExpectQuery("INSERT INTO box_messages").
		WithArgs(sqlmock.AnyArg(), "account-a", "task-1", "duplicate", "agent-message:e9").
		WillReturnRows(boxMessageRow("message-dup", "task-1", "", "agent", "duplicate", "delivered"))
	mock.ExpectExec("UPDATE box_tasks SET agent_busy=false").WithArgs("account-a", "task-1", "message-1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	server := chatTestServer(store)
	task := v1.BoxTask{ID: "task-1", LogicalBoxID: "box-1", Agent: "claude", Session: "claude-1"}
	event := boxruntime.ChatEvent{ID: "e9", Kind: "reply", ReplyTo: "chat-key-1", Text: "duplicate"}
	messageID, matched, err := server.applyChatEvent(context.Background(), &chatDrainProvider{}, "service-1", "account-a", task, event)
	if err != nil || matched || messageID != "message-dup" {
		t.Fatalf("messageID=%q matched=%v err=%v", messageID, matched, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyChatEventDeduplicatesIdenticalAnsweredReply(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("FROM box_messages").WithArgs("account-a", "task-1", "chat-key-1").
		WillReturnRows(boxMessageRow("message-1", "task-1", "user-a", "user", "hello", "delivered"))
	mock.ExpectQuery("FROM box_messages").WithArgs("account-a", "agent-reply:message-1").
		WillReturnRows(boxMessageRow("reply-1", "task-1", "", "agent", "answer", "delivered"))
	mock.ExpectExec("UPDATE box_tasks SET agent_busy=false").WithArgs("account-a", "task-1", "message-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	server := chatTestServer(store)
	task := v1.BoxTask{ID: "task-1", LogicalBoxID: "box-1", Agent: "claude", Session: "claude-1"}
	event := boxruntime.ChatEvent{ID: "retry-event", Kind: "reply", ReplyTo: "chat-key-1", Text: "answer"}
	messageID, matched, err := server.applyChatEvent(context.Background(), &chatDrainProvider{}, "service-1", "account-a", task, event)
	if err != nil || !matched || messageID != "message-1" {
		t.Fatalf("messageID=%q matched=%v err=%v", messageID, matched, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestApplyChatEventStoresUncorrelatedMessage covers chat_message without a
// replyTo: nothing is resolved and the text lands as its own agent message.
func TestApplyChatEventStoresUncorrelatedMessage(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("INSERT INTO box_messages").
		WithArgs(sqlmock.AnyArg(), "account-a", "task-1", "status update", "agent-message:e3").
		WillReturnRows(boxMessageRow("message-3", "task-1", "", "agent", "status update", "delivered"))
	mock.ExpectExec("UPDATE box_tasks SET agent_busy").WithArgs("account-a", "task-1", false).
		WillReturnResult(sqlmock.NewResult(0, 1))

	server := chatTestServer(store)
	task := v1.BoxTask{ID: "task-1", LogicalBoxID: "box-1", Agent: "claude", Session: "claude-1"}
	event := boxruntime.ChatEvent{ID: "e3", Kind: "reply", Text: "status update"}
	messageID, matched, err := server.applyChatEvent(context.Background(), &chatDrainProvider{}, "service-1", "account-a", task, event)
	if err != nil || matched || messageID != "message-3" {
		t.Fatalf("messageID=%q matched=%v err=%v", messageID, matched, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
