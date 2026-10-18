package controller

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestCreationFailurePostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx := context.Background()
	base, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "creation_failure_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err := base.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.DB.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	store, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner, err := store.Bootstrap(ctx, "creation-failure-fixture", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	boxID, slotID := uuid(), uuid()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO compute_slots(id,account_id,provider,ordinal,state,service_id,assignment_generation,fencing_token) VALUES($1,$2,'fake',1,'reserved','disposable',1,'fence')`, slotID, owner.AccountID)
	exec(`INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,slot_id,assignment_generation,fencing_token,restoration_state) VALUES($1,$2,$3,'creation-failure-fixture','fake','attaching','disposable','disposable',$4,1,'fence','creation-verifying-profiles')`, boxID, owner.AccountID, owner.UserID, slotID)
	creation := logicalBoxCreation{AccountID: owner.AccountID, Assignment: fleetAssignment{Box: v1.LogicalBox{ID: boxID, AssignmentGeneration: 1}, Slot: v1.ComputeSlot{ID: slotID}, FencingToken: "fence"}}
	// Interrupted transport remains recoverable with the same volume and fence.
	if err := store.FailLogicalBoxCreation(ctx, creation, "transport interrupted", false); err != nil {
		t.Fatal(err)
	}
	recoverable, err := store.RecoverableLogicalBoxCreations(ctx)
	if err != nil || len(recoverable) != 1 {
		t.Fatalf("recovery=%d err=%v", len(recoverable), err)
	}
	// Rejected credentials/model become terminal immediately and cannot replay.
	reason := "selected OpenCode model is unavailable"
	if err := store.FailLogicalBoxCreation(ctx, creation, reason, true); err != nil {
		t.Fatal(err)
	}
	recoverable, err = store.RecoverableLogicalBoxCreations(ctx)
	if err != nil || len(recoverable) != 0 {
		t.Fatalf("terminal recovery=%d err=%v", len(recoverable), err)
	}
	var state, phase, failure, volume, slot string
	if err := store.DB.QueryRowContext(ctx, `SELECT state,restoration_state,failure_reason,volume_id,slot_id::text FROM logical_boxes WHERE id=$1`, boxID).Scan(&state, &phase, &failure, &volume, &slot); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || phase != "creation-failed" || failure != reason || volume != "disposable" || slot != slotID {
		t.Fatalf("state=%s phase=%s failure=%s volume=%s slot=%s", state, phase, failure, volume, slot)
	}
	// Stale completion cannot erase the failure or make the slot available.
	if err := store.UpdateLogicalBoxCreationPhase(ctx, creation, "creation-initializing"); err == nil {
		t.Fatal("terminal phase was overwritten")
	}
	if err := store.FailLogicalBoxCreation(ctx, creation, "stale retry", false); err == nil {
		t.Fatal("terminal failure was overwritten")
	}
	if err := store.DB.QueryRowContext(ctx, `SELECT state,failure_reason FROM compute_slots WHERE id=$1`, slotID).Scan(&state, &failure); err != nil {
		t.Fatal(err)
	}
	if state != "draining" || failure != reason {
		t.Fatalf("slot state=%s reason=%s", state, failure)
	}
}
