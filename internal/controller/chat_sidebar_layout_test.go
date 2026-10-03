package controller

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestChatSidebarLayoutPersistsByAccount(t *testing.T) {
	store, mock := testStore(t)
	groups := `[ {"id":"group-1","name":"Projects","collapsed":true} ]`
	members := `{"box:box-1":"group-1"}`
	mutes := `{"group:group-1":null}`
	pins := `["box:box-1"]`
	sections := `{"boxes":true}`
	mock.ExpectExec("INSERT INTO chat_sidebar_layouts").WithArgs("account-a", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	server := &Server{Store: store}
	w := httptest.NewRecorder()
	server.chatSidebarLayoutHandler(w, httptest.NewRequest(http.MethodPut, "/v1/chat-sidebar-layout", strings.NewReader(`{"groups":`+groups+`,"members":`+members+`,"mutes":`+mutes+`,"pins":`+pins+`,"sections":`+sections+`}`)), Principal{AccountID: "account-a"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Projects"`) {
		t.Fatalf("PUT status=%d body=%s", w.Code, w.Body.String())
	}
	mock.ExpectQuery("SELECT groups_json,members_json,mutes_json,pins_json,sections_json FROM chat_sidebar_layouts").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"groups_json", "members_json", "mutes_json", "pins_json", "sections_json"}).AddRow([]byte(groups), []byte(members), []byte(mutes), []byte(pins), []byte(sections)))
	w = httptest.NewRecorder()
	server.chatSidebarLayoutHandler(w, httptest.NewRequest(http.MethodGet, "/v1/chat-sidebar-layout", nil), Principal{AccountID: "account-a"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"collapsed":true`) || !strings.Contains(w.Body.String(), `"box:box-1":"group-1"`) || !strings.Contains(w.Body.String(), `"group:group-1":null`) || !strings.Contains(w.Body.String(), `"pins":["box:box-1"]`) || !strings.Contains(w.Body.String(), `"sections":{"boxes":true}`) {
		t.Fatalf("GET status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMutedAgentReplyDoesNotSchedulePush(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("SELECT members_json,mutes_json FROM chat_sidebar_layouts").WithArgs("account-a").WillReturnRows(
		sqlmock.NewRows([]string{"members_json", "mutes_json"}).AddRow([]byte(`{}`), []byte(`{"box:box-1":null}`)))
	(&Server{Store: store}).pushAgentReply(context.Background(), "account-a", v1.BoxTask{LogicalBoxID: "box-1", BoxName: "Builder"}, "Completed")
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPushLookupFailureDoesNotBypassMute(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("SELECT members_json,mutes_json FROM chat_sidebar_layouts").WithArgs("account-a").WillReturnError(sql.ErrConnDone)
	(&Server{Store: store, Logger: slog.Default()}).pushAgentReply(context.Background(), "account-a", v1.BoxTask{LogicalBoxID: "box-1"}, "Completed")
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
	mock.ExpectQuery("SELECT groups_json,members_json,mutes_json,pins_json,sections_json FROM chat_sidebar_layouts").WithArgs("account-b").WillReturnError(sql.ErrNoRows)
	w := httptest.NewRecorder()
	(&Server{Store: store}).chatSidebarLayoutHandler(w, httptest.NewRequest(http.MethodGet, "/v1/chat-sidebar-layout", nil), Principal{AccountID: "account-b"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"exists":false`) || !strings.Contains(w.Body.String(), `"groups":[]`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChatSidebarMutesValidateAndSuppressPush(t *testing.T) {
	for _, body := range []string{
		`{"groups":[],"members":{},"mutes":{"section:pairs":null}}`,
		`{"groups":[],"members":{},"mutes":{"pair:a/b":null}}`,
		`{"groups":[],"members":{},"mutes":{"group:missing":null}}`,
		`{"groups":[],"members":{},"mutes":{"box:a":"invalid"}}`,
	} {
		store, mock := testStore(t)
		w := httptest.NewRecorder()
		(&Server{Store: store}).chatSidebarLayoutHandler(w, httptest.NewRequest(http.MethodPut, "/v1/chat-sidebar-layout", strings.NewReader(body)), Principal{AccountID: "account-a"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d", body, w.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
	rows := func(mutes string) *sqlmock.Rows {
		return sqlmock.NewRows([]string{"members_json", "mutes_json"}).AddRow([]byte(`{"box:grouped":"focus"}`), []byte(mutes))
	}
	for _, test := range []struct {
		name, box, mutes string
		want             bool
	}{
		{"direct", "direct", `{"box:direct":null}`, true},
		{"group", "grouped", `{"group:focus":null}`, true},
		{"chats includes pinned", "pinned", `{"section:boxes":null}`, true},
		{"legacy pinned section no longer mutes", "pinned", `{"section:pinned":null}`, false},
		{"expired", "direct", `{"box:direct":"2020-01-01T00:00:00Z"}`, false},
		{"unmuted", "other", `{}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, mock := testStore(t)
			mock.ExpectQuery("SELECT members_json,mutes_json FROM chat_sidebar_layouts").WithArgs("account-a").WillReturnRows(rows(test.mutes))
			muted, err := (&Server{Store: store}).isBoxPushMuted(context.Background(), "account-a", test.box)
			if err != nil || muted != test.want {
				t.Fatalf("muted=%v err=%v", muted, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
	if !activeChatMute(map[string]*time.Time{"box:always": nil}, "box:always", time.Now()) {
		t.Fatal("null must mean always muted")
	}
}
