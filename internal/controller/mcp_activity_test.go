package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAgentToolActivityStoresSafeChatBullet(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec("INSERT INTO box_events").WithArgs(sqlmock.AnyArg(), "account", "box", "MCP · heartbeat · stop", "mcp:box:abcdef123456").WillReturnResult(sqlmock.NewResult(0, 1))
	request := httptest.NewRequest(http.MethodPost, "/v1/agent-desktop/tool-activity", strings.NewReader(`{"id":"abcdef123456","tool":"heartbeat","action":"stop"}`))
	request.SetPathValue("id", "box")
	response := httptest.NewRecorder()
	NewServer(store, nil).agentToolActivityHandler(response, request, Principal{AccountID: "account", Role: "desktop-agent"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"stored":true`) {
		t.Fatalf("activity response=%d %s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentToolActivityStoresOwnNonReplyChatBullet(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec("INSERT INTO box_events").WithArgs(sqlmock.AnyArg(), "account", "box", "MCP · chat_message", "mcp:box:abcdef123456").WillReturnResult(sqlmock.NewResult(0, 1))
	request := httptest.NewRequest(http.MethodPost, "/v1/agent-desktop/tool-activity", strings.NewReader(`{"id":"abcdef123456","tool":"chat_message"}`))
	request.SetPathValue("id", "box")
	response := httptest.NewRecorder()
	NewServer(store, nil).agentToolActivityHandler(response, request, Principal{AccountID: "account", Role: "desktop-agent"})
	if response.Code != http.StatusOK {
		t.Fatalf("own-chat activity status=%d", response.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
