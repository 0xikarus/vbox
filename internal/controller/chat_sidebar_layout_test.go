package controller

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestChatSidebarLayoutPersistsByAccount(t *testing.T) {
	store, mock := testStore(t)
	groups := `[ {"id":"group-1","name":"Projects","collapsed":true} ]`
	members := `{"box:box-1":"group-1"}`
	mock.ExpectExec("INSERT INTO chat_sidebar_layouts").WithArgs("account-a", sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	server := &Server{Store: store}
	w := httptest.NewRecorder()
	server.chatSidebarLayoutHandler(w, httptest.NewRequest(http.MethodPut, "/v1/chat-sidebar-layout", strings.NewReader(`{"groups":`+groups+`,"members":`+members+`}`)), Principal{AccountID: "account-a"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Projects"`) {
		t.Fatalf("PUT status=%d body=%s", w.Code, w.Body.String())
	}
	mock.ExpectQuery("SELECT groups_json,members_json FROM chat_sidebar_layouts").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"groups_json", "members_json"}).AddRow([]byte(groups), []byte(members)))
	w = httptest.NewRecorder()
	server.chatSidebarLayoutHandler(w, httptest.NewRequest(http.MethodGet, "/v1/chat-sidebar-layout", nil), Principal{AccountID: "account-a"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"collapsed":true`) || !strings.Contains(w.Body.String(), `"box:box-1":"group-1"`) {
		t.Fatalf("GET status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChatSidebarLayoutRejectsUnknownGroup(t *testing.T) {
	store, mock := testStore(t)
	w := httptest.NewRecorder()
	(&Server{Store: store}).chatSidebarLayoutHandler(w, httptest.NewRequest(http.MethodPut, "/v1/chat-sidebar-layout", strings.NewReader(`{"groups":[],"members":{"box:box-1":"missing"}}`)), Principal{AccountID: "account-a"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChatSidebarLayoutDistinguishesUnsavedAccount(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("SELECT groups_json,members_json FROM chat_sidebar_layouts").WithArgs("account-b").WillReturnError(sql.ErrNoRows)
	w := httptest.NewRecorder()
	(&Server{Store: store}).chatSidebarLayoutHandler(w, httptest.NewRequest(http.MethodGet, "/v1/chat-sidebar-layout", nil), Principal{AccountID: "account-b"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"exists":false`) || !strings.Contains(w.Body.String(), `"groups":[]`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
