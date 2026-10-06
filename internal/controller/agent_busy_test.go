package controller

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAgentBusyHandlerUpdatesOnlyTheBoundBoxSession(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec(`UPDATE box_tasks SET agent_busy`).
		WithArgs("account-a", "box-a", "codex-chat", true).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)UPDATE box_messages m SET state='read'.*t.session_name=\$3.*m.state IN \('delivering','delivered','ambiguous'\)`).
		WithArgs("account-a", "box-a", "codex-chat").
		WillReturnResult(sqlmock.NewResult(0, 1))
	server := chatTestServer(store)
	request := httptest.NewRequest(http.MethodPost, "/v1/agent-desktop/busy", bytes.NewBufferString(`{"session":"codex-chat","busy":true}`))
	request.SetPathValue("id", "box-a")
	response := httptest.NewRecorder()
	server.agentBusyHandler(response, request, Principal{AccountID: "account-a", Role: "desktop-agent", Subject: "desktop-box:box-a"})
	if response.Code != http.StatusOK {
		t.Fatalf("busy update returned %d: %s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestIdleEventDoesNotMarkMessagesRead(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec(`UPDATE box_tasks SET agent_busy`).WithArgs("account-a", "box-a", "codex-chat", false).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.SetBoxSessionBusy(context.Background(), "account-a", "box-a", "codex-chat", false); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReplyMarksItsParentReadIncludingAmbiguousDelivery(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec(`(?s)WITH target AS.*UPDATE box_messages parent SET state='read'.*parent.direction IN \('user','box'\).*parent.state<>'read'`).
		WithArgs(sqlmock.AnyArg(), "account-a", "task-a", "reply", "delivered", "agent-reply:message-a", "message-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	changed, err := store.UpsertAgentBoxMessage(context.Background(), "account-a", "task-a", "message-a", "reply", "delivered")
	if err != nil || !changed {
		t.Fatalf("changed=%t err=%v", changed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeChannelReceiptMarksReadOnlyAfterConsumption(t *testing.T) {
	for _, value := range []struct {
		agent  string
		submit bool
		want   string
	}{
		{"claude", true, "read"},
		{"claude", false, "delivered"},
		{"codex", true, "read"},
		{"opencode", true, "read"},
	} {
		if got := confirmedMessageState(value.agent, value.submit); got != value.want {
			t.Errorf("%s submit=%t: got %s, want %s", value.agent, value.submit, got, value.want)
		}
	}
}

func TestBoxAgentBusyDistinguishesExplicitAndLegacyState(t *testing.T) {
	t.Run("explicit", func(t *testing.T) {
		store, mock := testStore(t)
		stamp := time.Now().UTC()
		mock.ExpectQuery(`SELECT agent_busy,agent_busy_updated_at FROM box_tasks`).WithArgs("account-a", "box-a").
			WillReturnRows(sqlmock.NewRows([]string{"agent_busy", "agent_busy_updated_at"}).AddRow(true, stamp))
		busy, known, updated, err := store.BoxAgentBusy(context.Background(), "account-a", "box-a")
		if err != nil || !known || !busy || !updated.Equal(stamp) {
			t.Fatalf("busy=%t known=%t updated=%v err=%v", busy, known, updated, err)
		}
	})
	t.Run("legacy null", func(t *testing.T) {
		store, mock := testStore(t)
		mock.ExpectQuery(`SELECT agent_busy,agent_busy_updated_at FROM box_tasks`).WithArgs("account-a", "box-a").
			WillReturnRows(sqlmock.NewRows([]string{"agent_busy", "agent_busy_updated_at"}).AddRow(nil, nil))
		busy, known, _, err := store.BoxAgentBusy(context.Background(), "account-a", "box-a")
		if err != nil || known || busy {
			t.Fatalf("busy=%t known=%t err=%v", busy, known, err)
		}
	})
	t.Run("no active chat", func(t *testing.T) {
		store, mock := testStore(t)
		mock.ExpectQuery(`SELECT agent_busy,agent_busy_updated_at FROM box_tasks`).WithArgs("account-a", "box-a").WillReturnError(sql.ErrNoRows)
		busy, known, _, err := store.BoxAgentBusy(context.Background(), "account-a", "box-a")
		if err != nil || !known || busy {
			t.Fatalf("busy=%t known=%t err=%v", busy, known, err)
		}
	})
}

func TestDeliveredUserMessageMarksItsAgentBusy(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec(`(?s)WITH message AS.*state<>'read'.*RETURNING id,task_id,direction,submit.*agent_busy_message_id=message.id`).WithArgs("account-a", "message-a", "delivered", "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.SetBoxMessageState(context.Background(), "account-a", "message-a", "delivered", ""); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOlderReplyDoesNotClearNewerBusyGeneration(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec(`UPDATE box_tasks SET agent_busy=false`).WithArgs("account-a", "task-a", "message-a").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := store.SetBoxTaskIdleForMessage(context.Background(), "account-a", "task-a", "message-a"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
