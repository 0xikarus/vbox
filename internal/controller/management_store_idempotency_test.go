package controller

import (
	"context"
	"testing"
)

func TestDirectBoxMessageByKeyIncludesSenderBoxColumn(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "account-a", UserID: "user-a", Role: "user"}
	mock.ExpectQuery(`COALESCE\(m.sender_box_id::text,''\).*FROM box_messages`).
		WithArgs("account-a", "box-1", "key:task:initial", "key:message", "user-a", "user").
		WillReturnRows(boxMessageRow("message-1", "task-1", "user-a", "user", "hello", "delivered"))
	mock.ExpectQuery("FROM box_tasks t JOIN logical_boxes b").
		WithArgs("account-a", "task-1", "user-a", "user").
		WillReturnRows(boxTaskRowWithSession("task-1", "active", "opencode-one"))

	task, message, found, err := store.DirectBoxMessageByKey(context.Background(), p, "box-1", "key")
	if err != nil || !found || task.ID != "task-1" || message.ID != "message-1" {
		t.Fatalf("task=%+v message=%+v found=%t err=%v", task, message, found, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
