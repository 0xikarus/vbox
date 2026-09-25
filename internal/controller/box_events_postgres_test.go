package controller

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestBoxEventHistoryWithoutTaskPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.DB.SetMaxOpenConns(1)
	schema := "box_events_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err := store.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer store.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	if _, err := store.DB.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := store.Bootstrap(ctx, "box-events-test", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	boxID := uuid()
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,'fixture','railway','hibernated','fixture-volume','fixture-volume')`, boxID, p.AccountID, p.UserID); err != nil {
		t.Fatal(err)
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendBoxEvent(ctx, tx, p.AccountID, boxID, "hibernated · workspace saved", "hibernate:"+boxID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/v1/logical-boxes/"+boxID+"/messages", nil)
	request.SetPathValue("id", boxID)
	response := httptest.NewRecorder()
	NewServer(store, nil).boxMessageHistory(response, request, p)
	if response.Code != 200 {
		t.Fatalf("history status %d: %s", response.Code, response.Body.String())
	}
	var messages []v1.BoxMessage
	if err := json.Unmarshal(response.Body.Bytes(), &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Direction != "system" || messages[0].Text != "hibernated · workspace saved" {
		t.Fatalf("unexpected event history: %+v", messages)
	}
}
