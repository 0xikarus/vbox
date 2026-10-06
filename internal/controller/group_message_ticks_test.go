package controller

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGroupDeliveryExposesRecipientReadStateAndUpdatedTime(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"}
	created := time.Now().UTC().Add(-time.Minute)
	read := created.Add(30 * time.Second)
	mock.ExpectQuery(`SELECT m.id::text,m.group_id::text`).WithArgs("account-a", "message-a", "user-a", "owner").
		WillReturnRows(sqlmock.NewRows([]string{"id", "group_id", "user_id", "source_box_id", "source_name", "body", "created_at", "parent_message_id", "thread_id"}).AddRow("message-a", "group-a", "user-a", "", "", "Hello", created, "", "message-a"))
	mock.ExpectQuery(`(?s)COALESCE\(bm.state,d.state\).*GREATEST\(d.updated_at,bm.updated_at\)`).WithArgs("account-a", "message-a").
		WillReturnRows(sqlmock.NewRows([]string{"logical_box_id", "box_name", "task_id", "box_message_id", "state", "failure", "updated_at"}).AddRow("box-a", "Builder", "task-a", "box-message-a", "read", "", read))
	message, err := store.GroupMessage(context.Background(), p, "message-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Deliveries) != 1 || message.Deliveries[0].State != "read" || !message.Deliveries[0].UpdatedAt.Equal(read) {
		t.Fatalf("delivery=%+v", message.Deliveries)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
