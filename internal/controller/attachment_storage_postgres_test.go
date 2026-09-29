package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBoxAttachmentStoragePostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.DB.SetMaxOpenConns(1)
	ns := "attachment_storage_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err := store.DB.ExecContext(ctx, "CREATE SCHEMA "+ns); err != nil {
		t.Fatal(err)
	}
	defer store.DB.ExecContext(context.Background(), "DROP SCHEMA "+ns+" CASCADE")
	if _, err := store.DB.ExecContext(ctx, "SET search_path TO "+ns); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner, err := store.Bootstrap(ctx, "attachment-storage-test", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	first, second := uuid(), uuid()
	for _, box := range []struct{ id, name string }{{first, "First"}, {second, "Second"}} {
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,$4,'railway','hibernated',$5,$5)`, box.id, owner.AccountID, owner.UserID, box.name, "volume-"+box.id); err != nil {
			t.Fatal(err)
		}
	}
	shared, private, pending, unused := uuid(), uuid(), uuid(), uuid()
	for _, image := range []struct {
		id   string
		size int
	}{{shared, 5}, {private, 7}, {pending, 11}, {unused, 13}} {
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO run_once_images(id,account_id,media_type,data,download_token,expires_at) VALUES($1,$2,'image/png',$3,'test-token',now()+interval '1 day')`, image.id, owner.AccountID, make([]byte, image.size)); err != nil {
			t.Fatal(err)
		}
	}
	addMessage := func(box, state string, images ...string) string {
		t.Helper()
		task, message := uuid(), uuid()
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_tasks(id,account_id,logical_box_id,user_id,requested_role,agent,session_name,prompt,state,idempotency_key) VALUES($1,$2,$3,$4,'owner','codex','test','test','active',$5)`, task, owner.AccountID, box, owner.UserID, "task-"+task); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_messages(id,account_id,task_id,user_id,direction,body,state,idempotency_key) VALUES($1,$2,$3,$4,'user','keep this text',$5,$6)`, message, owner.AccountID, task, owner.UserID, state, "message-"+message); err != nil {
			t.Fatal(err)
		}
		for index, image := range images {
			if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_message_images(message_id,account_id,image_id,ordinal) VALUES($1,$2,$3,$4)`, message, owner.AccountID, image, index+1); err != nil {
				t.Fatal(err)
			}
		}
		return message
	}
	firstMessage := addMessage(first, "delivered", shared, private)
	addMessage(first, "queued", pending)
	addMessage(second, "delivered", shared)
	server := NewServer(store, nil)
	request := func(method, confirmation string) *httptest.ResponseRecorder {
		t.Helper()
		body := strings.NewReader(confirmation)
		r := httptest.NewRequest(method, "/v1/logical-boxes/"+first+"/attachment-storage", body).WithContext(ctx)
		r.SetPathValue("id", first)
		response := httptest.NewRecorder()
		server.boxAttachmentStorageHandler(response, r, owner)
		return response
	}
	before := request(http.MethodGet, "")
	if before.Code != http.StatusOK {
		t.Fatalf("usage HTTP %d: %s", before.Code, before.Body.String())
	}
	var usage boxAttachmentStorage
	if err := json.Unmarshal(before.Body.Bytes(), &usage); err != nil || usage.BoxBytes != 23 || usage.BoxCount != 3 || usage.ClearableCount != 2 || usage.AccountBytes != 36 || usage.UnusedBytes != 13 || usage.UnusedCount != 1 {
		t.Fatalf("usage=%+v error=%v", usage, err)
	}
	if rejected := request(http.MethodDelete, `{"confirmation":"wrong"}`); rejected.Code != http.StatusConflict {
		t.Fatalf("wrong confirmation HTTP %d", rejected.Code)
	}
	cleared := request(http.MethodDelete, `{"confirmation":"First"}`)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear HTTP %d: %s", cleared.Code, cleared.Body.String())
	}
	var result struct {
		FreedBytes        int64 `json:"freedBytes"`
		RemovedReferences int64 `json:"removedReferences"`
	}
	if err := json.Unmarshal(cleared.Body.Bytes(), &result); err != nil || result.FreedBytes != 7 || result.RemovedReferences != 2 {
		t.Fatalf("clear=%+v error=%v", result, err)
	}
	var text string
	if err := store.DB.QueryRowContext(ctx, `SELECT body FROM box_messages WHERE id=$1`, firstMessage).Scan(&text); err != nil || text != "keep this text" {
		t.Fatalf("chat text was lost: %q %v", text, err)
	}
	var remaining int
	if err := store.DB.QueryRowContext(ctx, `SELECT count(*) FROM box_message_images WHERE account_id=$1 AND image_id=$2`, owner.AccountID, pending).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("pending attachment lost: %d %v", remaining, err)
	}
	if err := store.DB.QueryRowContext(ctx, `SELECT count(*) FROM box_message_images WHERE account_id=$1 AND image_id=$2`, owner.AccountID, shared).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("other box's shared attachment lost: %d %v", remaining, err)
	}
	after := request(http.MethodGet, "")
	if err := json.Unmarshal(after.Body.Bytes(), &usage); err != nil || usage.BoxBytes != 11 || usage.BoxCount != 1 || usage.ClearableCount != 0 || usage.AccountBytes != 29 || usage.UnusedBytes != 13 || usage.UnusedCount != 1 {
		t.Fatalf("usage after clear=%+v error=%v", usage, err)
	}
}
