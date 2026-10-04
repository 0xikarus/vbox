package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

// TestMailDraftVisibilityPostgres runs the agent and owner routes against an
// isolated schema in a disposable PostgreSQL database.
func TestMailDraftVisibilityPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires a disposable PostgreSQL database")
	}
	t.Setenv("VMBOX_MAIL_DOMAIN", "example.test")
	ctx := context.Background()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.DB.SetMaxOpenConns(1)
	schema := "mail_visibility_" + strings.ReplaceAll(uuid(), "-", "")
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
	ownerToken := uuid()
	owner, err := store.Bootstrap(ctx, "mail-visibility", "mail-visibility-owner", ownerToken)
	if err != nil {
		t.Fatal(err)
	}
	box := uuid()
	fence := strings.Repeat("a", 64)
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,fencing_token) VALUES($1,$2,$3,'Draft box','railway','running','mail-visibility-volume','mail-visibility-volume',$4)`, box, owner.AccountID, owner.UserID, fence); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO agent_box_policies(account_id,box_id,capabilities,updated_by) VALUES($1,$2,'{"mail":{"compose":true},"mcpTools":{"enabled":true,"allowedTools":["send_email"]}}'::jsonb,$3)`, owner.AccountID, box, owner.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_mail_settings(account_id,box_id,enabled,address) VALUES($1,$2,true,'draft-box@example.test')`, owner.AccountID, box); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO mail_addresses(id,account_id,local_part,address,label,owning_box_id) VALUES($1,$2,'draft-box','draft-box@example.test','Draft box',$1)`, box, owner.AccountID); err != nil {
		t.Fatal(err)
	}
	shared := uuid()
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO mail_addresses(id,account_id,local_part,address,label) VALUES($1,$2,'shared','shared@example.test','Shared')`, shared, owner.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO mail_address_grants(account_id,address_id,box_id) VALUES($1,$2,$3)`, owner.AccountID, shared, box); err != nil {
		t.Fatal(err)
	}
	agentToken, err := store.IssueDesktopAgentToken(ctx, owner, box, fence)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store, provider.NewRegistry()).Handler()
	call := func(method, path, token, body string, desktop bool) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if desktop {
			req.Header.Set("Authorization", "DesktopAgent "+token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		var value map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &value); err != nil {
			t.Fatalf("%s %s: status %d body %s: %v", method, path, resp.Code, resp.Body.String(), err)
		}
		return resp.Code, value
	}
	for _, draft := range []struct{ key, from string }{{"own", ""}, {"shared", `,"from":"shared@example.test"`}} {
		status, value := call(http.MethodPost, "/v1/agent-desktop/mail/outbox", agentToken, `{"to":["recipient@example.net"],"subject":"Visibility `+draft.key+`","text":"Review this", "idempotencyKey":"`+draft.key+`"`+draft.from+`}`, true)
		if status != 202 {
			t.Fatalf("agent submit %s: %d %+v", draft.key, status, value)
		}
	}
	for _, path := range []string{
		"/v1/logical-boxes/" + box + "/mail/outbox?status=pending_approval",
		"/v1/mail/outbox?status=pending_approval",
		"/v1/mail/outbox?status=pending_approval&box=" + box,
		"/v1/mail/approvals",
	} {
		status, value := call(http.MethodGet, path, ownerToken, "", false)
		if status != 200 {
			t.Fatalf("owner list %s: %d %+v", path, status, value)
		}
		key := "items"
		if strings.HasSuffix(path, "approvals") {
			if value["pending"] != float64(2) {
				t.Fatalf("approval count: %+v", value)
			}
		}
		if items, ok := value[key].([]any); !ok || len(items) != 2 {
			t.Fatalf("owner list %s: %+v", path, value)
		}
		if strings.Contains(path, "outbox") {
			items := value[key].([]any)
			from := map[string]bool{}
			for _, raw := range items {
				item := raw.(map[string]any)
				from[item["from"].(string)] = true
			}
			if !from["draft-box@example.test"] || !from["shared@example.test"] {
				t.Fatalf("outbox lost own or shared sender at %s: %+v", path, items)
			}
		}
	}
	if _, err := store.DB.ExecContext(ctx, `UPDATE box_mail_settings SET enabled=false WHERE box_id=$1`, box); err != nil {
		t.Fatal(err)
	}
	if status, value := call(http.MethodPost, "/v1/agent-desktop/mail/outbox", agentToken, `{"to":["recipient@example.net"],"subject":"Disabled","text":"No send", "idempotencyKey":"disabled"}`, true); status != 403 {
		t.Fatalf("disabled box submitted a draft: %d %+v", status, value)
	}
	if _, err := store.DB.ExecContext(ctx, `DELETE FROM box_mail_settings WHERE box_id=$1`, box); err != nil {
		t.Fatal(err)
	}
	if status, value := call(http.MethodPost, "/v1/agent-desktop/mail/outbox", agentToken, `{"to":["recipient@example.net"],"subject":"No settings","text":"No send", "idempotencyKey":"no-settings"}`, true); status != 403 {
		t.Fatalf("box without settings submitted a draft: %d %+v", status, value)
	}
	for _, path := range []string{"/v1/logical-boxes/" + box + "/mail/outbox?status=pending_approval", "/v1/mail/outbox?status=pending_approval", "/v1/mail/outbox?status=pending_approval&box=" + box} {
		status, value := call(http.MethodGet, path, ownerToken, "", false)
		if items, ok := value["items"].([]any); status != 200 || !ok || len(items) != 2 {
			t.Fatalf("drafts disappeared without settings row at %s: %d %+v", path, status, value)
		}
	}
}
