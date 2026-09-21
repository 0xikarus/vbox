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
	mock.ExpectExec(`WITH message AS`).WithArgs("account-a", "message-a", "delivered", "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.SetBoxMessageState(context.Background(), "account-a", "message-a", "delivered", ""); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
