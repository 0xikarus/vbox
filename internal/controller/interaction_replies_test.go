package controller

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestExtractCodexReplyAfterUpdatePrompt(t *testing.T) {
	content := `✨ Update available! 0.153.0 -> 0.153.2

› 1. Update now
  2. Skip

› What's today's date?

• Today is September 4, 2026.

› Ask Codex to do anything

  gpt-5.6-sol high · /data/workspace`
	reply, complete := extractAgentReply("codex", "What's today's date?", content)
	if !complete || reply != "Today is September 4, 2026." {
		t.Fatalf("complete=%v reply=%q", complete, reply)
	}
}

func TestExtractClaudeReplyOmitsTerminalChrome(t *testing.T) {
	content := `❯ What's today's date?

● Today's date is September 4, 2026 (Friday).

✻ Brewed for 1s · done 10:28 AM

────────────────────────────────────────
❯
────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle)
   ✘ Auto-update failed: no write permission`
	reply, complete := extractAgentReply("claude", "What's today's date?", content)
	if !complete || reply != "Today's date is September 4, 2026 (Friday)." {
		t.Fatalf("complete=%v reply=%q", complete, reply)
	}
}

func TestExtractAgentReplyWaitsForInputPrompt(t *testing.T) {
	content := "❯ Explain the result\n\n● Still working through the details"
	if reply, complete := extractAgentReply("claude", "Explain the result", content); complete || reply != "Still working through the details" {
		t.Fatalf("in-progress reply was not exposed: complete=%v reply=%q", complete, reply)
	}
}

func TestExtractAgentReplyUsesNewestMatchingPrompt(t *testing.T) {
	content := `› Repeat

• First answer.

› Ask Codex to do anything

› Repeat

• Second answer.

› Ask Codex to do anything`
	reply, complete := extractAgentReply("codex", "Repeat", content)
	if !complete || reply != "Second answer." {
		t.Fatalf("complete=%v reply=%q", complete, reply)
	}
}

func TestExtractAgentReplyCompletesWhenNextInputIsAlreadyStaged(t *testing.T) {
	content := `❯ Inspect authentication

● Authentication is ready.

❯ list my repos`
	reply, complete := extractAgentReply("claude", "Inspect authentication", content)
	if !complete || reply != "Authentication is ready." {
		t.Fatalf("complete=%v reply=%q", complete, reply)
	}
}

func TestAgentReplyProgressFinalizesWhenCapturedBoundaryScrollsAway(t *testing.T) {
	var progress agentReplyProgress
	if reply, state, done := progress.observe("partial output", false); reply != "partial output" || state != "streaming" || done {
		t.Fatalf("initial observation=(%q,%q,%v)", reply, state, done)
	}
	if reply, state, done := progress.observe("", false); reply != "" || state != "" || done {
		t.Fatalf("first missing boundary=(%q,%q,%v)", reply, state, done)
	}
	if reply, state, done := progress.observe("", false); reply != "partial output" || state != "delivered" || !done {
		t.Fatalf("second missing boundary=(%q,%q,%v)", reply, state, done)
	}
}

func TestAgentReplyProgressRequiresStableCompleteOutput(t *testing.T) {
	var progress agentReplyProgress
	if _, state, done := progress.observe("answer", true); state != "streaming" || done {
		t.Fatalf("first complete observation state=%q done=%v", state, done)
	}
	if _, state, done := progress.observe("answer", true); state != "delivered" || !done {
		t.Fatalf("stable complete observation state=%q done=%v", state, done)
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
