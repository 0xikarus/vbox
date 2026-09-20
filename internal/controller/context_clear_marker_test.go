package controller

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRecordContextClearAppendsOneRoutableSystemMarker(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Server{Store: &Store{DB: db}}
	for i := 0; i < 2; i++ {
		mock.ExpectExec("INSERT INTO box_messages").
			WithArgs(sqlmock.AnyArg(), "account-a", "task-a", "context cleared · opencode is ready", "context-clear:reset-a", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))
		if err := s.recordContextClear(context.Background(), "account-a", "task-a", "opencode", "reset-a"); err != nil {
			t.Fatal(err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
