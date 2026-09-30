package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestSaveContactImagesCreatesBoundedAttachmentReferences(t *testing.T) {
	store, mock := testStore(t)
	pixel, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC")
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec(`DELETE FROM run_once_images i`).WithArgs("account").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id::text FROM accounts WHERE id=\$1 FOR UPDATE`).WithArgs("account").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("account"))
	mock.ExpectQuery(`SELECT COALESCE\(sum\(octet_length\(data\)\),0\) FROM run_once_images`).WithArgs("account").WillReturnRows(sqlmock.NewRows([]string{"used"}).AddRow(0))
	mock.ExpectExec(`INSERT INTO run_once_images`).WithArgs(sqlmock.AnyArg(), "account", "image/png", pixel, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	refs, err := store.saveContactImages(context.Background(), "account", []boxruntime.ChatEventImage{{MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(pixel)}})
	if err != nil || len(refs) != 1 || refs[0].ID == "" || refs[0].Number != 1 {
		t.Fatalf("refs=%+v err=%v", refs, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// chatDrainProvider replays scripted chat-pull output and records every runtime
// call so a test can assert exactly which outbox events were acknowledged.
type chatDrainProvider struct {
	fakeProvider
	pulls       []string
	acks        []string
	namingCalls int
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
	case "chat-codex-name":
		p.namingCalls++
		return provider.ExecResult{ExitCode: 1}, nil
	}
	return provider.ExecResult{}, nil
}

func TestCodexReplyDeliveryDoesNotDependOnThreadNaming(t *testing.T) {
	store, mock := testStore(t)
	prov := &chatDrainProvider{pulls: []string{chatEventJSON(t, boxruntime.ChatEvent{ID: "e1", Kind: "reply", ReplyTo: "chat-key-1", Text: "answer"})}}
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
	task := v1.BoxTask{ID: "task-1", LogicalBoxID: "box-1", Agent: "codex", Session: "codex-1"}
	done, err := chatTestServer(store).pullStructuredAgentReply(context.Background(), prov, "service-1", "account-a", task, v1.BoxMessage{ID: "message-1"})
	if err != nil || !done || len(prov.acks) != 1 || prov.namingCalls != 0 {
		t.Fatalf("done=%v err=%v acks=%v namingCalls=%d", done, err, prov.acks, prov.namingCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
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

func TestChatReadyDirectEventConfirmsStoredReply(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").WithArgs("account-a", "box-1").WillReturnRows(restartTestTaskRow("task-1", "active"))
	mock.ExpectQuery("FROM box_messages").WithArgs("account-a", "task-1", "chat-key-1").
		WillReturnRows(boxMessageRow("message-1", "task-1", "user-a", "user", "hello", "delivered"))
	mock.ExpectQuery("FROM box_messages").WithArgs("account-a", "agent-reply:message-1").
		WillReturnRows(boxMessageRow("reply-1", "task-1", "", "agent", "answer", "delivered"))
	mock.ExpectExec("UPDATE box_tasks SET agent_busy=false").WithArgs("account-a", "task-1", "message-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	r := httptest.NewRequest(http.MethodPost, "/v1/agent-desktop/chat-ready", strings.NewReader(`{"session":"opencode-one","event":{"id":"0123456789ab","kind":"reply","replyTo":"chat-key-1","text":"answer"}}`))
	r.SetPathValue("id", "box-1")
	w := httptest.NewRecorder()
	chatTestServer(store).agentChatReadyHandler(w, r, Principal{AccountID: "account-a", UserID: "user-a", Role: "desktop-agent"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"stored":true`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChatReadyContactReportsHibernatedRejectionToSender(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").WithArgs("account-a", "box-1").WillReturnRows(restartTestTaskRow("task-1", "active"))
	mock.ExpectQuery("FROM logical_boxes b").WithArgs("account-a", "mascot").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "default_agent", "state", "protected"}).AddRow("box-2", "mascot", "claude", "hibernated", false))
	mock.ExpectExec("INSERT INTO box_messages").WithArgs(sqlmock.AnyArg(), "account-a", "task-1", "Contact message rejected: mascot is hibernated; only a running box can receive a message", "contact-reject:0123456789ab", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	r := httptest.NewRequest(http.MethodPost, "/v1/agent-desktop/chat-ready", strings.NewReader(`{"session":"opencode-one","event":{"id":"0123456789ab","kind":"contact","contact":"mascot","text":"hello"}}`))
	r.SetPathValue("id", "box-1")
	w := httptest.NewRecorder()
	chatTestServer(store).agentChatReadyHandler(w, r, Principal{AccountID: "account-a", UserID: "user-a", Role: "desktop-agent"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"delivered":false`) || !strings.Contains(w.Body.String(), `"reason":"mascot is hibernated`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChatReadyContactKeepsTransientFailureForRetry(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").WithArgs("account-a", "box-1").WillReturnRows(restartTestTaskRow("task-1", "active"))
	mock.ExpectQuery("FROM logical_boxes b").WithArgs("account-a", "mascot").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "agent", "state", "protected"}).AddRow("target", "mascot", "codex", "running", false))
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT b.state,EXISTS").WithArgs("account-a", "target").WillReturnRows(sqlmock.NewRows([]string{"state", "protected"}).AddRow("running", false))
	mock.ExpectQuery("SELECT can_message FROM box_contacts").WithArgs("account-a", "box-1", "target").WillReturnRows(sqlmock.NewRows([]string{"can_message"}).AddRow(true))
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM box_role_assignments").WithArgs("account-a", "box-1").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery("SELECT owner_user_id::text FROM logical_boxes").WithArgs("account-a", "target").WillReturnRows(sqlmock.NewRows([]string{"owner_user_id"}).AddRow("user-a"))
	mock.ExpectQuery("FROM box_messages m JOIN box_tasks").WillReturnError(errors.New("temporary database failure"))
	r := httptest.NewRequest(http.MethodPost, "/v1/agent-desktop/chat-ready", strings.NewReader(`{"session":"opencode-one","event":{"id":"0123456789ab","kind":"contact","contact":"mascot","text":"hello"}}`))
	r.SetPathValue("id", "box-1")
	w := httptest.NewRecorder()
	chatTestServer(store).agentChatReadyHandler(w, r, Principal{AccountID: "account-a", UserID: "user-a", Role: "desktop-agent"})
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), `"stored":true`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestContactWorkerProbeFailureDoesNotRejectMessage(t *testing.T) {
	store, mock := testStore(t)
	server := chatTestServer(store)
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) {
		return &sessionProbeProvider{err: errors.New("worker command preparation failed")}, nil
	}
	mock.ExpectQuery("FROM logical_boxes b").WithArgs("account-a", "mascot").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "agent", "state", "protected"}).AddRow("box-1", "mascot", "opencode", "running", false))
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM logical_boxes").WithArgs("account-a", "sender").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT b.state,EXISTS").WithArgs("account-a", "box-1").WillReturnRows(sqlmock.NewRows([]string{"state", "protected"}).AddRow("running", false))
	mock.ExpectQuery("SELECT can_message FROM box_contacts").WithArgs("account-a", "sender", "box-1").WillReturnRows(sqlmock.NewRows([]string{"can_message"}).AddRow(true))
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM box_role_assignments").WithArgs("account-a", "sender").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery("SELECT owner_user_id::text FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(sqlmock.NewRows([]string{"owner_user_id"}).AddRow("user-a"))
	mock.ExpectQuery("FROM box_messages m JOIN box_tasks").WillReturnRows(emptyBoxMessageRows())
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("FROM box_notes").WithArgs("account-a", "box-1", "contact:0123456789ab").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "body", "created_at"}))
	mock.ExpectQuery("FROM box_messages m JOIN box_tasks").WillReturnRows(emptyBoxMessageRows())
	mock.ExpectQuery("SELECT COALESCE\\(metadata").WithArgs("account-a", "box-1").WillReturnRows(sqlmock.NewRows([]string{"session", "agent"}).AddRow("opencode-one", "opencode"))
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").WithArgs("account-a", "box-1").WillReturnRows(restartTestTaskRow("task-1", "active"))
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(restartTestBoxRow())
	mock.ExpectQuery("SELECT COALESCE\\(fencing_token").WithArgs("account-a", "box-1").WillReturnRows(sqlmock.NewRows([]string{"fencing_token"}).AddRow("fence"))
	messageID, reason, err := server.routeContactMessage(context.Background(), "account-a", v1.BoxTask{ID: "source-task", LogicalBoxID: "sender"}, boxruntime.ChatEvent{ID: "0123456789ab", Contact: "mascot", Text: "hello"})
	if err == nil || !strings.Contains(err.Error(), "worker command preparation failed") || messageID != "" || reason != "" {
		t.Fatalf("messageID=%q reason=%q err=%v", messageID, reason, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
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
