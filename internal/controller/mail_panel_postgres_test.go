package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

// TestMailPanelDefaultListPostgres exercises the owner routes with real SQL.
// Browser fixtures cannot catch a disagreement between summary and list SQL.
func TestMailPanelDefaultListPostgres(t *testing.T) {
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
	schema := "mail_panel_" + strings.ReplaceAll(uuid(), "-", "")
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
	owner, err := store.Bootstrap(ctx, "mail-panel", "mail-panel-owner", ownerToken)
	if err != nil {
		t.Fatal(err)
	}
	boxIDs := []string{uuid(), uuid()}
	addresses := []string{"first@example.test", "second@example.test"}
	for i, boxID := range boxIDs {
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,fencing_token) VALUES($1,$2,$3,$4,'railway','running',$5,$5,$6)`, boxID, owner.AccountID, owner.UserID, []string{"First", "Second"}[i], "mail-panel-volume-"+boxID, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_mail_settings(account_id,box_id,enabled,address) VALUES($1,$2,true,$3)`, owner.AccountID, boxID, addresses[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO mail_addresses(id,account_id,local_part,address,label,owning_box_id) VALUES($1,$2,$3,$4,$5,$1)`, boxID, owner.AccountID, []string{"first", "second"}[i], addresses[i], []string{"First", "Second"}[i]); err != nil {
			t.Fatal(err)
		}
	}
	extraID := uuid()
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO mail_addresses(id,account_id,local_part,address,label) VALUES($1,$2,'shared','shared@example.test','Shared')`, extraID, owner.AccountID); err != nil {
		t.Fatal(err)
	}
	for i, mailbox := range []struct {
		boxID              any
		addressID, address string
	}{
		{boxIDs[0], boxIDs[0], addresses[0]},
		{boxIDs[0], boxIDs[0], addresses[0]},
		{boxIDs[1], boxIDs[1], addresses[1]},
		{nil, extraID, "shared@example.test"},
	} {
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO mail_messages(id,account_id,box_id,address_id,ingest_key,raw_sha256,envelope_from,envelope_to,header_from,subject,preview) VALUES($1,$2,$3,$4,$5,$6,'sender@example.net',$7,'Sender',$8,'Preview')`, uuid(), owner.AccountID, mailbox.boxID, mailbox.addressID, "panel-"+string(rune('a'+i)), strings.Repeat("a", 64), mailbox.address, "Message "+string(rune('A'+i))); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewServer(store, provider.NewRegistry()).Handler()
	call := func(path string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, response.Code, response.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	summary := call("/v1/mail/summary")
	if summary["inbox"] != float64(4) || summary["unread"] != float64(4) {
		t.Fatalf("wrong summary: %+v", summary)
	}
	boxCounts := map[string]float64{}
	for _, raw := range summary["boxes"].([]any) {
		box := raw.(map[string]any)
		boxCounts[box["boxId"].(string)] = box["unread"].(float64)
	}
	if boxCounts[boxIDs[0]] != 2 || boxCounts[boxIDs[1]] != 1 {
		t.Fatalf("per-box counts should leave the unassigned message out: %+v", summary)
	}
	for _, check := range []struct {
		path string
		want int
	}{
		{"/v1/mail/messages?folder=all", 4},
		{"/v1/mail/messages?folder=unread", 4},
		{"/v1/mail/messages?folder=all&box=" + boxIDs[0], 2},
		{"/v1/mail/messages?folder=all&box=" + boxIDs[1], 1},
		{"/v1/mail/messages?folder=all&address=shared@example.test", 1},
	} {
		body := call(check.path)
		items, ok := body["messages"].([]any)
		if !ok || len(items) != check.want {
			t.Fatalf("inbox count is %v but %s returned %+v, want %d messages", summary["inbox"], check.path, body, check.want)
		}
	}
	if os.Getenv("VMBOX_MAIL_BROWSER_REAL") == "1" {
		server := httptest.NewServer(handler)
		defer server.Close()
		cmd := exec.CommandContext(ctx, "node", "tests/browser/mail-panel-real-handler.mjs")
		cmd.Dir = "../.."
		cmd.Env = append(os.Environ(), "MAIL_PANEL_REAL_ORIGIN="+server.URL, "MAIL_PANEL_REAL_TOKEN="+ownerToken)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("real-handler browser check: %v\n%s", err, output)
		} else {
			t.Logf("%s", output)
		}
	}
}
