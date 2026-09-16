package controller

import (
	"context"
	"crypto/sha256"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	sharedprovider "github.com/0xikarus/vmbox-service/internal/provider/shared"
	"github.com/0xikarus/vmbox-service/internal/transport"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestSharedWorkerControllerPostgres(t *testing.T) {
	dsn, endpoint := os.Getenv("VMBOX_TEST_DATABASE_URL"), os.Getenv("VMBOX_TEST_SHARED_DISPOSABLE_ENDPOINT")
	if dsn == "" || endpoint == "" {
		t.Skip("requires disposable shared worker and PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "shared_worker_" + strings.ReplaceAll(uuid(), "-", "")
	config.RuntimeParams["search_path"] = schema
	store := &Store{DB: stdlib.OpenDB(*config)}
	defer store.Close()
	if _, err := store.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer store.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	principal := Principal{AccountID: "00000000-0000-4000-8000-000000000001", UserID: uuid(), Role: "owner", Subject: "shared-disposable-test"}
	token := "disposable-controller-" + uuid()
	digest := sha256.Sum256([]byte(token))
	if _, err := store.DB.ExecContext(ctx, "INSERT INTO accounts(id,name) VALUES($1,'shared-disposable-test')", principal.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, "INSERT INTO users(id,account_id,subject,role) VALUES($1,$2,$3,'owner')", principal.UserID, principal.AccountID, principal.Subject); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, "INSERT INTO access_tokens(id,account_id,user_id,token_hash) VALUES($1,$2,$3,$4)", uuid(), principal.AccountID, principal.UserID, digest[:]); err != nil {
		t.Fatal(err)
	}
	prov, err := sharedprovider.New(endpoint, os.Getenv("VMBOX_TEST_SHARED_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(store, nil)
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return prov, nil }
	server.WorkerRuntime, err = os.ReadFile(os.Getenv("VMBOX_TEST_SHARED_RUNTIME"))
	if err != nil {
		t.Fatal(err)
	}
	fleet, err := store.SetFleetConfig(ctx, principal, v1.FleetConfig{Provider: "shared-worker", ComputeBoxSlots: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.reconcileFleet(ctx, principal.AccountID, fleet); err != nil {
		t.Fatal(err)
	}
	var assignments []fleetAssignment
	for _, name := range []string{"shared-first", "shared-second"} {
		creation, err := store.BeginLogicalBoxCreation(ctx, principal, v1.CreateLogicalBoxRequest{Name: name, Provider: "shared-worker", DefaultAgent: "shell", DiskGiB: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := server.finishLogicalBoxCreation(ctx, creation); err != nil {
			t.Fatal(err)
		}
		assignment, err := store.assignment(ctx, principal.AccountID, creation.Assignment.Box.ID)
		if err != nil {
			t.Fatal(err)
		}
		if assignment.Box.State != v1.LogicalBoxRunning {
			t.Fatalf("box not running: %s", assignment.Box.State)
		}
		assignments = append(assignments, assignment)
	}
	if assignments[0].Slot.ServiceID == assignments[1].Slot.ServiceID || assignments[0].Box.VolumeID == assignments[1].Box.VolumeID {
		t.Fatal("controller reused occupied slot or workspace")
	}
	tls := httptest.NewTLSServer(server.Handler())
	defer tls.Close()
	client := transport.Worker{ControllerURL: tls.URL, Token: token, HTTP: tls.Client()}
	for _, assignment := range assignments {
		connection, err := prov.Connection(ctx, assignment.Slot.ServiceID)
		if err != nil || !connectionMatchesAssignment(connection, assignment) {
			t.Fatalf("assignment not fenced: %v", err)
		}
		result, err := client.ExecConnection(ctx, clientWorkerConnection(connection, assignment), []string{"sh", "-c", "printf controller-shared-ok"}, provider.ExecOptions{})
		if err != nil || result.Stdout != "controller-shared-ok" || result.ExitCode != 0 {
			t.Fatalf("controller relay: %+v, %v", result, err)
		}
		result, err = prov.Exec(ctx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "interactive-start", nativeFence(assignment), "shared-test-shell", "shell"}, provider.ExecOptions{})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("interactive start: %+v, %v", result, err)
		}
	}
	first := assignments[0]
	if _, err := store.BeginLogicalBoxRelease(ctx, principal, first.Box.ID, v1.LogicalBoxHibernating); err != nil {
		t.Fatal(err)
	}
	if err := server.resumeLogicalBoxHibernate(ctx, principal, first.Box.ID); err != nil {
		t.Fatal(err)
	}
	box, err := store.LogicalBox(ctx, principal, first.Box.ID)
	if err != nil || box.State != v1.LogicalBoxHibernated {
		t.Fatalf("hibernate: %+v, %v", box, err)
	}
	result, err := prov.Exec(ctx, assignments[1].Slot.ServiceID, []string{"tmux", "has-session", "-t", "shared-test-shell"}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("sibling disrupted: %+v, %v", result, err)
	}
	allocation, err := store.ReserveAllocation(ctx, principal, first.Box.ID, "shared-resume", "shared-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.activateAllocation(ctx, principal.AccountID, allocation, false); err != nil {
		t.Fatal(err)
	}
	if _, err := server.queueLogicalBoxDelete(ctx, principal, first.Box.ID, first.Box.Name); err != nil {
		t.Fatal(err)
	}
	if err := server.resumeLogicalBoxDelete(ctx, principal, first.Box.ID); err != nil {
		t.Fatal(err)
	}
	result, err = prov.Exec(ctx, assignments[1].Slot.ServiceID, []string{"tmux", "has-session", "-t", "shared-test-shell"}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("sibling disrupted by delete: %+v, %v", result, err)
	}
	slots, err := prov.List(ctx)
	if err != nil || len(slots) != 2 {
		t.Fatalf("box deletion removed shared compute: %v, %v", slots, err)
	}
}
