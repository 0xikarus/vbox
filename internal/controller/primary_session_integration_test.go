package controller

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestPrimarySessionPostgres(t *testing.T) {
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
	schema := "primary_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = s.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer s.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	if _, err = s.DB.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := s.Bootstrap(ctx, "primary-test", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	box, slot := uuid(), uuid()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,ordinal,state,service_id,assignment_generation,fencing_token) VALUES($1,$2,'railway',1,'occupied','test-service',1,'fence')`, slot, p.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,slot_id,assignment_generation,fencing_token,metadata) VALUES($1,$2,$3,'primary-box','railway','running','test-volume','test-volume',$4,1,'fence','{"unrelated":"preserved"}')`, box, p.AccountID, p.UserID, slot)
	if err != nil {
		t.Fatal(err)
	}
	a := fleetAssignment{Box: v1.LogicalBox{ID: box, AssignmentGeneration: 1}, Slot: v1.ComputeSlot{ID: slot}, FencingToken: "fence"}
	if name, err := s.PrimarySession(ctx, p, box); err != nil || name != "" {
		t.Fatal(name, err)
	}
	if err = s.rememberPrimarySession(ctx, p, a, "old ü session"); err != nil {
		t.Fatal(err)
	}
	// A separate store instance reads persisted state, not an in-memory preference.
	fresh := &Store{DB: s.DB}
	if name, err := fresh.PrimarySession(ctx, p, box); err != nil || name != "old ü session" {
		t.Fatal(name, err)
	}
	var unrelated string
	if err = s.DB.QueryRowContext(ctx, `SELECT metadata->>'unrelated' FROM logical_boxes WHERE id=$1`, box).Scan(&unrelated); err != nil || unrelated != "preserved" {
		t.Fatal(unrelated, err)
	}
	a.Box.AssignmentGeneration++
	if err = s.rememberPrimarySession(ctx, p, a, "stale"); err == nil {
		t.Fatal("stale assignment accepted")
	}
	if _, err = fresh.PrimarySession(ctx, Principal{AccountID: uuid(), UserID: uuid()}, box); err == nil {
		t.Fatal("cross-account read accepted")
	}
	if name, err := fresh.PrimarySession(ctx, p, box); err != nil || name != "old ü session" {
		t.Fatal(name, err)
	}
}
