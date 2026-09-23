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

func TestContactConversationsPostgres(t *testing.T) {
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
	schema := "contact_transcript_test_" + strings.ReplaceAll(uuid(), "-", "")
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
	p, err := store.Bootstrap(ctx, "contact-transcript-test", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	a, b, taskA, taskB := uuid(), uuid(), uuid(), uuid()
	for _, value := range []struct{ id, name, task string }{{a, "Builder", taskA}, {b, "Reviewer", taskB}} {
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,$4,'railway','hibernated',$5,$5)`, value.id, p.AccountID, p.UserID, value.name, "volume-"+value.name); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_tasks(id,account_id,logical_box_id,user_id,requested_role,agent,session_name,prompt,state,idempotency_key) VALUES($1,$2,$3,$4,'owner','codex','contact-test','prompt','active',$5)`, value.task, p.AccountID, value.id, p.UserID, "task-"+value.name); err != nil {
			t.Fatal(err)
		}
	}
	m1, m2, m3, owner := uuid(), uuid(), uuid(), uuid()
	for _, value := range []struct{ id, task, sender, direction, text, parent string }{{m1, taskB, a, "box", "Please inspect", ""}, {m2, taskA, b, "box", "Done", ""}, {m3, taskB, "", "agent", "Direct answer", m1}, {owner, taskB, "", "user", "Owner only", ""}} {
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_messages(id,account_id,task_id,user_id,direction,body,state,idempotency_key,sender_box_id,parent_message_id,thread_id) VALUES($1,$2,$3,$4,$5,$6,'delivered',$7,NULLIF($8,'')::uuid,NULLIF($9,'')::uuid,$1)`, value.id, p.AccountID, value.task, p.UserID, value.direction, value.text, "message-"+value.id, value.sender, value.parent); err != nil {
			t.Fatal(err)
		}
	}
	imageID := uuid()
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO run_once_images(id,account_id,media_type,data,download_token,expires_at) VALUES($1,$2,'image/png',$3,'test-token',now()+interval '1 day')`, imageID, p.AccountID, []byte("image")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_message_images(message_id,account_id,image_id,ordinal) VALUES($1,$2,$3,1)`, m1, p.AccountID, imageID); err != nil {
		t.Fatal(err)
	}
	server := NewServer(store, nil)
	list := httptest.NewRecorder()
	server.contactConversationsHandler(list, httptest.NewRequest("GET", "/v1/box-conversations", nil), p)
	if list.Code != 200 {
		t.Fatalf("pair list: %d %s", list.Code, list.Body)
	}
	var pairs []contactConversation
	if err := json.Unmarshal(list.Body.Bytes(), &pairs); err != nil || len(pairs) != 1 {
		t.Fatalf("pairs=%+v err=%v", pairs, err)
	}
	request := httptest.NewRequest("GET", "/v1/box-conversations/"+a+"/"+b+"/messages", nil)
	request.SetPathValue("a", a)
	request.SetPathValue("b", b)
	response := httptest.NewRecorder()
	server.contactConversationMessagesHandler(response, request, p)
	if response.Code != 200 {
		t.Fatalf("conversation: %d %s", response.Code, response.Body)
	}
	var messages []contactConversationMessage
	if err := json.Unmarshal(response.Body.Bytes(), &messages); err != nil || len(messages) != 3 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	byID := map[string]contactConversationMessage{}
	for _, message := range messages {
		byID[message.ID] = message
	}
	if byID[m1].SenderBoxID != a || byID[m1].RecipientBoxID != b || len(byID[m1].Images) != 1 {
		t.Fatalf("first contact=%+v", byID[m1])
	}
	if byID[m3].SenderBoxID != b || byID[m3].RecipientBoxID != a {
		t.Fatalf("direct agent reply=%+v", byID[m3])
	}
	ownerRequest := httptest.NewRequest("GET", "/v1/logical-boxes/"+b+"/messages", nil)
	ownerRequest.SetPathValue("id", b)
	ownerResponse := httptest.NewRecorder()
	server.boxMessageHistory(ownerResponse, ownerRequest, p)
	if ownerResponse.Code != 200 {
		t.Fatalf("owner history: %d %s", ownerResponse.Code, ownerResponse.Body)
	}
	var ownerMessages []v1.BoxMessage
	if err := json.Unmarshal(ownerResponse.Body.Bytes(), &ownerMessages); err != nil || len(ownerMessages) != 1 || ownerMessages[0].ID != owner {
		t.Fatalf("owner messages=%+v err=%v", ownerMessages, err)
	}
}
