package controller

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestAgentBoxWorkerPlacementAcrossPoolsPostgres(t *testing.T) {
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
	schema := "agent_pool_test_" + strings.ReplaceAll(uuid(), "-", "")
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
	owner, err := store.Bootstrap(ctx, "agent-pool-test", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	for _, pool := range []struct {
		name    string
		desired int
	}{
		{name: "creator", desired: 1},
		{name: "other", desired: 1},
		{name: "disabled", desired: 0},
	} {
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO provider_credentials(id,account_id,provider,name,encrypted_value) VALUES($1,$2,'shared-worker',$3,'fixture')`, uuid(), owner.AccountID, pool.name); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO fleet_settings(account_id,provider,provider_credential,compute_box_slots) VALUES($1,'shared-worker',$2,$3)`, owner.AccountID, pool.name, pool.desired); err != nil {
			t.Fatal(err)
		}
	}
	otherSlot := uuid()
	for _, slot := range []struct {
		id, pool, state string
	}{
		{id: uuid(), pool: "creator", state: "occupied"},
		{id: otherSlot, pool: "other", state: "free"},
		{id: uuid(), pool: "disabled", state: "free"},
	} {
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,provider_credential,ordinal,state,health) VALUES($1,$2,'shared-worker',$3,1,$4,'healthy')`, slot.id, owner.AccountID, slot.pool, slot.state); err != nil {
			t.Fatal(err)
		}
	}
	workers, err := store.availableAgentBoxWorkers(ctx, owner.AccountID, "shared-worker", "creator")
	if err != nil || len(workers) != 1 || workers[0].SlotID != otherSlot || workers[0].ProviderCredential != "other" {
		t.Fatalf("workers=%+v error=%v", workers, err)
	}
	candidates, err := store.agentBoxPlacementCandidates(ctx, owner.AccountID, "shared-worker", "creator", otherSlot)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].SlotID != otherSlot {
		t.Fatalf("explicit candidate=%+v", candidates)
	}
	automatic, err := store.agentBoxPlacementCandidates(ctx, owner.AccountID, "shared-worker", "creator", "")
	if err != nil || len(automatic) != 1 || automatic[0].ProviderCredential != "other" {
		t.Fatalf("automatic candidates=%+v error=%v", automatic, err)
	}
	selected := automatic[0]
	creation, err := store.BeginLogicalBoxCreation(ctx, owner, v1.CreateLogicalBoxRequest{
		Name: "child", Provider: selected.Provider, ProviderCredential: selected.ProviderCredential,
		DefaultAgent: "shell", DiskGiB: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if creation.Assignment.Box.ProviderCredential != "other" || creation.Assignment.Slot.ID != otherSlot {
		t.Fatalf("creation placed on wrong pool: %+v", creation.Assignment)
	}
}
