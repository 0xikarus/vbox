package controller

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestSavedNewBoxAutoStartSurvivesControllerRestartPostgres(t *testing.T) {
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
	schema := "auto_start_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = store.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer store.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	if _, err = store.DB.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner, err := store.Bootstrap(ctx, "auto-start-test", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,ordinal,state,service_id,region,health)
 VALUES($1,$2,'railway',1,'free','disposable-start-slot','region-a','healthy')`, uuid(), owner.AccountID); err != nil {
		t.Fatal(err)
	}
	newBox, explicitHibernate := uuid(), uuid()
	for _, box := range []struct {
		id, name, metadata string
	}{
		{newBox, "automatic", `{"allocateWhenReady":true,"region":"region-a"}`},
		{explicitHibernate, "explicit-hibernate", `{"allocateWhenReady":false,"region":"region-a"}`},
	} {
		if _, err = store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,restoration_state,metadata)
 VALUES($1,$2,$3,$4,'railway','hibernated',$5,$5,'saved',$6::jsonb)`, box.id, owner.AccountID, owner.UserID, box.name, "disposable-volume-"+box.name, box.metadata); err != nil {
			t.Fatal(err)
		}
	}
	server := NewServer(store, nil)
	if err := server.ReconcileLogicalBoxCreationsNow(ctx); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		id   string
		want v1.LogicalBoxState
	}{
		{newBox, v1.LogicalBoxReserved},
		{explicitHibernate, v1.LogicalBoxHibernated},
	} {
		var state v1.LogicalBoxState
		if err := store.DB.QueryRowContext(ctx, `SELECT state FROM logical_boxes WHERE id=$1`, check.id).Scan(&state); err != nil || state != check.want {
			t.Fatalf("box %s state=%s want=%s err=%v", check.id, state, check.want, err)
		}
	}
	pending, err := store.PendingAutoStarts(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("startup intent replayed: pending=%v err=%v", pending, err)
	}
}
