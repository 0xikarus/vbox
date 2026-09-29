package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestSaveAgentResumeDecisionRecordsChatEvent(t *testing.T) {
	for _, test := range []struct {
		agent, choice, event string
	}{
		{"codex", "restore", "Saved Codex session restored after wake"},
		{"claude", "fresh", "Fresh Claude session chosen after wake"},
		{"opencode", "restore", "Saved OpenCode session restored after wake"},
	} {
		t.Run(test.agent+"-"+test.choice, func(t *testing.T) {
			store, mock := testStore(t)
			savedAt := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
			candidate := boxruntime.ResumeCandidate{SessionID: "session-a", SavedAt: savedAt}
			assignment := fleetAssignment{Box: v1.LogicalBox{ID: "box-a", AssignmentGeneration: 3}, Slot: v1.ComputeSlot{ID: "slot-a"}, FencingToken: "fence-a"}
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE logical_boxes SET metadata=jsonb_set").WithArgs("account-a", "box-a", sqlmock.AnyArg(), "slot-a", int64(3), "fence-a", test.agent+"ResumeDecision", savedAt.Format(time.RFC3339Nano)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO box_events").WithArgs(sqlmock.AnyArg(), "account-a", "box-a", test.event, "agent-resume:box-a:"+test.agent+":session-a:"+savedAt.Format(time.RFC3339Nano)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			saved, err := store.saveAgentResumeDecision(context.Background(), Principal{AccountID: "account-a"}, assignment, test.agent, candidate, test.choice)
			if err != nil || !saved {
				t.Fatalf("saved=%t err=%v", saved, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSaveAgentResumeDecisionRollsBackIfChatEventFails(t *testing.T) {
	store, mock := testStore(t)
	candidate := boxruntime.ResumeCandidate{SessionID: "session-a", SavedAt: time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)}
	assignment := fleetAssignment{Box: v1.LogicalBox{ID: "box-a", AssignmentGeneration: 3}, Slot: v1.ComputeSlot{ID: "slot-a"}, FencingToken: "fence-a"}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE logical_boxes SET metadata=jsonb_set").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO box_events").WillReturnError(errors.New("event write failed"))
	mock.ExpectRollback()
	saved, err := store.saveAgentResumeDecision(context.Background(), Principal{AccountID: "account-a"}, assignment, "codex", candidate, "restore")
	if saved || err == nil {
		t.Fatalf("saved=%t err=%v", saved, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
