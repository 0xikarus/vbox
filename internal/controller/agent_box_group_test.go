package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCreatedBoxInheritsCreatorSidebarGroup(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO chat_sidebar_layouts").WithArgs("account-a").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT groups_json,members_json FROM chat_sidebar_layouts").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"groups_json", "members_json"}).AddRow([]byte(`[{"id":"team-1","name":"Builders","collapsed":true}]`), []byte(`{"box:creator":"team-1"}`)))
	mock.ExpectExec("UPDATE chat_sidebar_layouts SET groups_json").WithArgs("account-a", `[{"id":"team-1","name":"Builders","collapsed":true}]`, `{"box:child":"team-1","box:creator":"team-1"}`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := addCreatedBoxToSidebarGroup(context.Background(), tx, "account-a", "creator", "Creator", "child"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestNewCreatedBoxGroupNameAvoidsExistingNamesAndLimit(t *testing.T) {
	name := createdBoxGroupName("Architect", []chatSidebarGroup{{ID: "other", Name: "architect TEAM"}})
	if name != "Architect team 2" {
		t.Fatalf("group name=%q", name)
	}
	if got := createdBoxGroupName(strings.Repeat("x", 80), nil); len([]rune(got)) != 48 {
		t.Fatalf("group name length=%d", len([]rune(got)))
	}
}

func TestUngroupedCreatorAndChildJoinNewGroup(t *testing.T) {
	layout := chatSidebarLayout{Groups: []chatSidebarGroup{{ID: "other", Name: "Architect team"}}, Members: map[string]string{"box:someone-else": "other"}}
	if err := groupCreatedBox(&layout, "creator", "Architect", "child"); err != nil {
		t.Fatal(err)
	}
	if len(layout.Groups) != 2 || layout.Groups[1].Name != "Architect team 2" || layout.Members["box:creator"] == "" || layout.Members["box:creator"] != layout.Members["box:child"] || layout.Members["box:someone-else"] != "other" {
		t.Fatalf("layout=%+v", layout)
	}
}
