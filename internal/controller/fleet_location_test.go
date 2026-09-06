package controller

import (
	"context"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFleetLocationPostgres(t *testing.T) {
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
	schema := "location_test_" + strings.ReplaceAll(uuid(), "-", "")
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
	p, err := s.Bootstrap(ctx, "location-test", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	config := v1.FleetConfig{Provider: "railway", ComputeBoxSlots: 2}
	if _, err = s.SetFleetConfig(ctx, p, config); err != nil {
		t.Fatal(err)
	}
	if err = s.SetFleetLocation(ctx, p, "railway", "", "region-a"); err == nil {
		t.Fatal("accepted nonzero desired capacity")
	}
	config.ComputeBoxSlots = 0
	if _, err = s.SetFleetConfig(ctx, p, config); err != nil {
		t.Fatal(err)
	}
	slot := uuid()
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,ordinal,state) VALUES($1,$2,'railway',1,'free')`, slot, p.AccountID); err != nil {
		t.Fatal(err)
	}
	if err = s.SetFleetLocation(ctx, p, "railway", "", "region-a"); err == nil {
		t.Fatal("accepted existing slot")
	}
	if _, err = s.DB.ExecContext(ctx, `DELETE FROM compute_slots WHERE id=$1`, slot); err != nil {
		t.Fatal(err)
	}
	if err = s.SetFleetLocation(ctx, p, "railway", "", "region-a"); err != nil {
		t.Fatal(err)
	}
	config.ComputeBoxSlots = 2
	if _, err = s.SetFleetConfig(ctx, p, config); err != nil {
		t.Fatal(err)
	}
	got, err := s.FleetConfig(ctx, p.AccountID, "railway", "")
	if err != nil || got.Region != "region-a" {
		t.Fatalf("region lost after scaling: %+v %v", got, err)
	}
	config.ComputeBoxSlots = 0
	if _, err = s.SetFleetConfig(ctx, p, config); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,'saved','railway','hibernated','volume','volume')`, uuid(), p.AccountID, p.UserID); err != nil {
		t.Fatal(err)
	}
	if err = s.SetFleetLocation(ctx, p, "railway", "", "region-b"); err == nil {
		t.Fatal("accepted stranded regional volume")
	}
	if err = s.SetFleetLocation(ctx, Principal{AccountID: uuid()}, "railway", "", "region-b"); err == nil {
		t.Fatal("accepted cross-account change")
	}
}
