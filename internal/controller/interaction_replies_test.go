package controller

import (
	"context"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

type replyTransportProvider struct {
	fakeProvider
	calls [][]string
}

func (p *replyTransportProvider) Exec(_ context.Context, _ string, argv []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	p.calls = append(p.calls, append([]string(nil), argv...))
	return provider.ExecResult{}, nil
}

func TestAgentChatDoesNotReadRepliesFromTerminalOutput(t *testing.T) {
	store, mock := testStore(t)
	now := time.Now().UTC()
	boxRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{
			"id", "account_id", "owner_user_id", "name", "provider", "provider_credential",
			"default_agent", "role", "state", "volume_id", "volume_name", "slot_id", "assignment_generation",
			"lease_owner", "lease_expires_at", "restoration_state", "failure_reason", "created_at", "updated_at", "tools",
		}).AddRow("box-1", "account-a", "user-a", "research", "fake", "primary", "opencode", "worker",
			string(v1.LogicalBoxRunning), "volume-1", "volume-name", "slot-1", int64(3), "", nil, "", "", now, now, "[]")
	}
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(boxRows())
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(boxRows())
	mock.ExpectQuery("SELECT COALESCE\\(fencing_token").WithArgs("account-a", "box-1").
		WillReturnRows(sqlmock.NewRows([]string{"fencing_token"}).AddRow("fence-1"))
	mock.ExpectQuery("FROM compute_slots").WithArgs("account-a", "slot-1").
		WillReturnRows(sqlmock.NewRows(computeSlotColumns()).AddRow(
			"slot-1", "account-a", "fake", "primary", 1, "occupied", "service-1", "slot-1",
			"deployment-1", "box-1", "research", "iad", "worker@sha256:digest", "", "healthy",
			int64(3), "", nil, "", now, now,
		))
	mock.ExpectQuery("FROM box_messages WHERE account_id").WithArgs("account-a", "agent-reply:message-1").
		WillReturnRows(emptyBoxMessageRows())
	// Another drain path can consume and store the outbox event between watcher
	// polls. The watcher must observe that durable reply and stop instead of
	// polling the now-empty outbox until its ten-minute timeout.
	mock.ExpectQuery("FROM box_messages WHERE account_id").WithArgs("account-a", "agent-reply:message-1").
		WillReturnRows(boxMessageRow("reply-1", "task-1", "", "agent", "answer", "delivered"))

	transport := &replyTransportProvider{}
	server := chatTestServer(store)
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return transport, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	task := v1.BoxTask{ID: "task-1", LogicalBoxID: "box-1", UserID: "user-a", RequestedRole: "user", Agent: "opencode", Session: "opencode-1"}
	if err := server.captureAgentReply(ctx, "account-a", task, v1.BoxMessage{ID: "message-1", Text: "hello"}); err != nil {
		t.Fatalf("watcher did not stop after the reply was delivered elsewhere: %v", err)
	}

	for _, call := range transport.calls {
		if len(call) > 1 && call[1] == "tmux-screen" {
			t.Fatalf("ordinary terminal output was used as an agent-chat reply: %v", call)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertAgentBoxMessageStreamsAndFinalizesCorrelatedReply(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec("INSERT INTO box_messages").
		WithArgs(sqlmock.AnyArg(), "account-a", "task-1", "working", "streaming", "agent-reply:message-1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	changed, err := store.UpsertAgentBoxMessage(context.Background(), "account-a", "task-1", "message-1", "working", "streaming")
	if err != nil || !changed {
		t.Fatalf("streamed=%v err=%v", changed, err)
	}
	mock.ExpectExec("INSERT INTO box_messages").
		WithArgs(sqlmock.AnyArg(), "account-a", "task-1", "answer", "delivered", "agent-reply:message-1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	changed, err = store.UpsertAgentBoxMessage(context.Background(), "account-a", "task-1", "message-1", "answer", "delivered")
	if err != nil || !changed {
		t.Fatalf("finalized=%v err=%v", changed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentBoxMessageRestoresStreamingReplyAfterWatcherRestart(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("FROM box_messages WHERE account_id").
		WithArgs("account-a", "agent-reply:message-1").
		WillReturnRows(boxMessageRow("reply-1", "task-1", "", "agent", "partial", "streaming"))
	message, found, err := store.AgentBoxMessage(context.Background(), "account-a", "message-1")
	if err != nil || !found || message.Text != "partial" || message.State != "streaming" {
		t.Fatalf("message=%+v found=%v err=%v", message, found, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentReplyWatchActiveStopsAfterBoxDeletion(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("SELECT EXISTS").WithArgs("account-a", "task-1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	active, err := store.AgentReplyWatchActive(context.Background(), "account-a", "task-1")
	if err != nil || active {
		t.Fatalf("active=%v err=%v", active, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUnansweredBoxMessagesExcludesCapturedReplies(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").
		WithArgs("account-a", "task-1", "user-a", "user").
		WillReturnRows(boxTaskRow("task-1", "active"))
	mock.ExpectQuery("reply.state='delivered'").WithArgs("account-a", "task-1").
		WillReturnRows(boxMessageRow("message-1", "task-1", "user-a", "user", "hello", "delivered"))
	messages, err := store.UnansweredBoxMessages(context.Background(), Principal{AccountID: "account-a", UserID: "user-a", Role: "user"}, "task-1")
	if err != nil || len(messages) != 1 || messages[0].ID != "message-1" {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyCodexImageSchemaFailure(t *testing.T) {
	for _, detail := range []string{
		"vmbox-runtime: codex app server: Invalid request: missing field `url`",
		"codex app server: unknown variant `localImage`",
	} {
		if !legacyCodexImageSchemaFailure(provider.ExecResult{ExitCode: 1, Stderr: detail}) {
			t.Fatalf("legacy schema error %q was not detected", detail)
		}
	}
	for _, result := range []provider.ExecResult{
		{ExitCode: 0, Stderr: "missing field `url`"},
		{ExitCode: 1, Stderr: "turn is already running"},
	} {
		if legacyCodexImageSchemaFailure(result) {
			t.Fatalf("unrelated result was treated as a legacy schema error: %+v", result)
		}
	}
}
