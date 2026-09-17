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
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestGridExcludesBusyAndOtherAccounts(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.DB.SetMaxOpenConns(1)
	schema := "grid_test_" + strings.ReplaceAll(uuid(), "-", "")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE SCHEMA " + schema)
	defer s.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	exec("SET search_path TO " + schema)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := s.Bootstrap(ctx, "grid", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Bootstrap(ctx, "other-grid", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	add := func(p Principal, name string) string {
		id := uuid()
		exec(`INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,$4,'railway','hibernated',$5,$4)`, id, p.AccountID, p.UserID, name, id)
		return id
	}
	persistent := add(p, "once-is-a-valid-interactive-name")
	busy := add(p, "busy")
	add(other, "foreign")
	id := uuid()
	result, _ := json.Marshal(v1.ProcessTask{ID: id, LogicalBoxID: busy, State: "running"})
	exec(`INSERT INTO process_tasks(id,account_id,logical_box_id,user_id,requested_role,idempotency_key,state,result) VALUES($1,$2,$3,$4,'owner','test','running',$5)`, id, p.AccountID, busy, p.UserID, result)
	server := NewServer(s, provider.NewRegistry())
	w := httptest.NewRecorder()
	server.gridBoxesHandler(w, httptest.NewRequest("GET", "/v1/grid-boxes", nil), p)
	var boxes []v1.LogicalBox
	if err = json.Unmarshal(w.Body.Bytes(), &boxes); err != nil || w.Code != 200 || len(boxes) != 1 || boxes[0].ID != persistent {
		t.Fatal(w.Code, w.Body.String(), err)
	}
}
