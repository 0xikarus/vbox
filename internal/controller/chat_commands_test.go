package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestChatCommandValidation(t *testing.T) {
	for _, name := range []string{"review", "test_2", "fix-bug"} {
		if !validChatCommandName(name) {
			t.Fatalf("rejected valid command %q", name)
		}
	}
	for _, name := range []string{"", "-review", "Review", "has space", strings.Repeat("x", 41)} {
		if validChatCommandName(name) {
			t.Fatalf("accepted invalid command %q", name)
		}
	}
}

func TestChatCommandHandlerSavesAccountScopedPrompt(t *testing.T) {
	store, mock := testStore(t)
	now := time.Now().UTC()
	mock.ExpectQuery("INSERT INTO chat_commands").WithArgs("account-a", "review", "Check the diff").
		WillReturnRows(sqlmock.NewRows([]string{"name", "prompt", "created_at", "updated_at"}).AddRow("review", "Check the diff", now, now))
	r := httptest.NewRequest(http.MethodPut, "/v1/chat-commands/review", strings.NewReader(`{"prompt":"Check the diff"}`))
	r.SetPathValue("name", "review")
	w := httptest.NewRecorder()
	(&Server{Store: store}).chatCommandHandler(w, r, Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"prompt":"Check the diff"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChatCommandHandlerRejectsInvalidPromptBeforeWrite(t *testing.T) {
	store, mock := testStore(t)
	r := httptest.NewRequest(http.MethodPut, "/v1/chat-commands/review", strings.NewReader(`{"prompt":"  "}`))
	r.SetPathValue("name", "review")
	w := httptest.NewRecorder()
	(&Server{Store: store}).chatCommandHandler(w, r, Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
