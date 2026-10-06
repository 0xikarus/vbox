package controller

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestReadMessageAttachmentsRemainClearable(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`(?s)bool_or\(m.state IN \('delivered','read'\)\) AS clearable`).
		WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"count", "bytes", "clearable"}).AddRow(1, 7, 1))
	mock.ExpectQuery(`SELECT COALESCE\(sum\(octet_length\(i.data\)\),0\)`).
		WithArgs("account-a").
		WillReturnRows(sqlmock.NewRows([]string{"account_bytes", "unused_bytes", "unused_count"}).AddRow(7, 0, 0))
	value, err := store.boxAttachmentStorage(context.Background(), "account-a", "box-a")
	if err != nil || value.ClearableCount != 1 || value.BoxBytes != 7 {
		t.Fatalf("value=%+v err=%v", value, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClearBoxAttachmentsIncludesReadMessages(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "account-a", UserID: "user-a"}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id::text FROM accounts WHERE id=\$1 FOR UPDATE`).WithArgs("account-a").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("account-a"))
	mock.ExpectQuery(`(?s)DELETE FROM box_message_images j.*m.state IN \('delivered','read'\)`).WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"image_id"}))
	mock.ExpectExec(`INSERT INTO audit_log`).WithArgs("account-a", "user-a", "box-a", int64(0), int64(0)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	freed, removed, err := store.clearBoxAttachments(context.Background(), p, "box-a")
	if err != nil || freed != 0 || removed != 0 {
		t.Fatalf("freed=%d removed=%d err=%v", freed, removed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
