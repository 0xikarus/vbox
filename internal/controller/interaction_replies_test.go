package controller

import (
	"context"
	"testing"
	"time"

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
	if reply, complete := extractAgentReply("claude", "Explain the result", content); complete || reply != "" {
		t.Fatalf("captured an in-progress reply: complete=%v reply=%q", complete, reply)
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

func TestAppendAgentBoxMessageIsCorrelatedAndIdempotent(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec("INSERT INTO box_messages").
		WithArgs(sqlmock.AnyArg(), "account-a", "task-1", "answer", "agent-reply:message-1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	inserted, err := store.AppendAgentBoxMessage(context.Background(), "account-a", "task-1", "message-1", "answer")
	if err != nil || !inserted {
		t.Fatalf("inserted=%v err=%v", inserted, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUnansweredBoxMessagesExcludesCapturedReplies(t *testing.T) {
	store, mock := testStore(t)
	now := time.Now().UTC()
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").
		WithArgs("account-a", "task-1", "user-a", "user").
		WillReturnRows(boxTaskRow("task-1", "active"))
	mock.ExpectQuery("NOT EXISTS").WithArgs("account-a", "task-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "user_id", "direction", "body", "state", "created_at", "updated_at"}).
			AddRow("message-1", "task-1", "user-a", "user", "hello", "delivered", now, now))
	messages, err := store.UnansweredBoxMessages(context.Background(), Principal{AccountID: "account-a", UserID: "user-a", Role: "user"}, "task-1")
	if err != nil || len(messages) != 1 || messages[0].ID != "message-1" {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
