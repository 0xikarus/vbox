package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

type ambiguousReplacementProvider struct {
	provider.Provider
	connection provider.Connection
	calls      int
}

func (p *ambiguousReplacementProvider) Name() string { return "railway" }
func (p *ambiguousReplacementProvider) Connection(context.Context, string) (provider.Connection, error) {
	if p.connection.Endpoint != "" || p.connection.Transport != "" || p.connection.Metadata != nil {
		return p.connection, nil
	}
	return provider.Connection{Transport: "openssh", Endpoint: "new-deployment@ssh.railway.com", Metadata: map[string]string{"deploymentInstanceId": "new-deployment"}}, nil
}
func (p *ambiguousReplacementProvider) ExecConnection(context.Context, provider.Connection, []string, provider.ExecOptions) (provider.ExecResult, error) {
	p.calls++
	return provider.ExecResult{}, errors.New("ambiguous transport interruption")
}

func replacementAssignment() fleetAssignment {
	a := fleetAssignment{FencingToken: "assignment-fence"}
	a.Box.ID = "box"
	a.Box.AccountID = "account"
	a.Box.Provider = "railway"
	a.Box.AssignmentGeneration = 4
	a.Slot.ID = "slot"
	return a
}

func expectReplacementAssignmentLock(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT b.assignment_generation FROM logical_boxes`).
		WithArgs("account", "box", "slot", int64(4), "assignment-fence").
		WillReturnRows(sqlmock.NewRows([]string{"assignment_generation"}).AddRow(int64(4)))
}

func TestWorkerReplacementRequiresVerifiedPriorDeployment(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectBegin()
	expectReplacementAssignmentLock(mock)
	mock.ExpectQuery(`SELECT id::text,account_id::text,slot_id::text`).
		WithArgs("account", "slot").
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "slot_id", "incarnation", "connection_epoch", "transport_enabled", "bootstrap_deployment_id", "replacement_deployment_instance_id", "enrollment", "credential", "live"}).
			AddRow("worker", "account", "slot", "old-incarnation", int64(7), true, "", "", false, true, true))
	mock.ExpectRollback()
	_, _, started, err := store.beginWorkerReplacement(context.Background(), "account", replacementAssignment(), "new-deployment")
	if err == nil || started {
		t.Fatal("replacement accepted missing prior deployment proof", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerReplacementRotationFencesOldCredentialAndEpoch(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectBegin()
	expectReplacementAssignmentLock(mock)
	mock.ExpectQuery(`SELECT id::text,account_id::text,slot_id::text`).
		WithArgs("account", "slot").
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "slot_id", "incarnation", "connection_epoch", "transport_enabled", "bootstrap_deployment_id", "replacement_deployment_instance_id", "enrollment", "credential", "live"}).
			AddRow("worker", "account", "slot", "old-incarnation", int64(7), true, "old-deployment", "", false, true, true))
	mock.ExpectExec(`UPDATE direct_workers SET enrollment_hash=.*credential_hash=NULL.*connection_epoch=connection_epoch\+1`).
		WithArgs(sqlmock.AnyArg(), "new-deployment", "worker", "account", "slot", int64(7), "old-incarnation").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	state, token, started, err := store.beginWorkerReplacement(context.Background(), "account", replacementAssignment(), "new-deployment")
	if err != nil || !started || !validWorkerSecret(token) || state.Worker.Epoch != 8 || state.Worker.Incarnation != "" || state.Credential || !state.Enrollment || state.Live {
		t.Fatalf("replacement rotation did not fence old authority: %+v started=%v err=%v", state, started, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerReplacementMissingAssignmentProofDoesNotRotate(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT b.assignment_generation FROM logical_boxes`).
		WithArgs("account", "box", "slot", int64(4), "assignment-fence").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	_, _, started, err := store.beginWorkerReplacement(context.Background(), "account", replacementAssignment(), "new-deployment")
	if !errors.Is(err, errWorkerIdentity) || started {
		t.Fatal("replacement rotated without assignment proof", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerReplacementRejectsUntrustedTargetBeforeRotation(t *testing.T) {
	for name, connection := range map[string]provider.Connection{
		"transport": {Transport: "railway-cli", Endpoint: "new-deployment@ssh.railway.com", Metadata: map[string]string{"deploymentInstanceId": "new-deployment"}},
		"endpoint":  {Transport: "openssh", Endpoint: "other-deployment@ssh.railway.com", Metadata: map[string]string{"deploymentInstanceId": "new-deployment"}},
		"syntax":    {Transport: "openssh", Endpoint: "bad/instance@ssh.railway.com", Metadata: map[string]string{"deploymentInstanceId": "bad/instance"}},
	} {
		t.Run(name, func(t *testing.T) {
			store, mock := testStore(t)
			mock.ExpectQuery(`SELECT EXISTS`).WithArgs("account", "slot").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
			server := NewServer(store, nil)
			server.PublicURL = "https://controller.example"
			server.WorkerAgent = []byte("replacement-agent")
			backing := &ambiguousReplacementProvider{connection: connection}
			if err := server.ensureReplacementWorkerTransport(context.Background(), "account", replacementAssignment(), backing); err == nil || err.Error() != "worker replacement deployment evidence unavailable" {
				t.Fatalf("untrusted replacement target did not fail before store mutation: %v", err)
			}
			if backing.calls != 0 {
				t.Fatalf("untrusted replacement target executed remotely: calls=%d", backing.calls)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal("untrusted replacement target mutated the store", err)
			}
		})
	}
}

func TestAmbiguousWorkerReplacementInstallIsNotReplayed(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`SELECT EXISTS`).WithArgs("account", "slot").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectBegin()
	expectReplacementAssignmentLock(mock)
	mock.ExpectQuery(`SELECT id::text,account_id::text,slot_id::text`).
		WithArgs("account", "slot").
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "slot_id", "incarnation", "connection_epoch", "transport_enabled", "bootstrap_deployment_id", "replacement_deployment_instance_id", "enrollment", "credential", "live"}).
			AddRow("worker", "account", "slot", "old-incarnation", int64(7), true, "old-deployment", "", false, true, true))
	mock.ExpectExec(`UPDATE direct_workers SET enrollment_hash=.*credential_hash=NULL.*connection_epoch=connection_epoch\+1`).
		WithArgs(sqlmock.AnyArg(), "new-deployment", "worker", "account", "slot", int64(7), "old-incarnation").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(`SELECT id::text,account_id::text,slot_id::text,incarnation,connection_epoch,transport_enabled`).
		WithArgs("worker", "account", "slot").WillReturnError(sql.ErrNoRows)
	server := NewServer(store, nil)
	server.PublicURL = "https://controller.example"
	server.WorkerAgent = []byte("replacement-agent")
	backing := &ambiguousReplacementProvider{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := server.ensureReplacementWorkerTransport(ctx, "account", replacementAssignment(), backing)
	if err == nil || backing.calls != 1 {
		t.Fatalf("ambiguous replacement replayed: calls=%d err=%v", backing.calls, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerReplacementPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.DB.SetMaxOpenConns(1)
	schema := "worker_replacement_test_" + strings.ReplaceAll(uuid(), "-", "")
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
	ownerToken := uuid()
	principal, err := store.Bootstrap(ctx, "replacement-account", "owner", ownerToken)
	if err != nil {
		t.Fatal(err)
	}
	slotID, boxID := uuid(), uuid()
	if _, err = store.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,ordinal,state,service_id,assignment_generation,fencing_token) VALUES($1,$2,'railway',1,'occupied','replacement-service',1,'replacement-fence')`, slotID, principal.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,slot_id,assignment_generation,fencing_token) VALUES($1,$2,$3,'replacement-box','railway','running','replacement-volume','replacement-volume',$4,1,'replacement-fence')`, boxID, principal.AccountID, principal.UserID, slotID); err != nil {
		t.Fatal(err)
	}
	worker, enrollment, err := store.IssueWorkerEnrollment(ctx, principal.AccountID, slotID)
	if err != nil {
		t.Fatal(err)
	}
	oldCredential, _ := secretToken()
	if _, err = store.ExchangeWorkerEnrollment(ctx, enrollment, oldCredential); err != nil {
		t.Fatal(err)
	}
	oldIncarnation, _ := secretToken()
	connectionOwner, _ := secretToken()
	oldWorker, err := store.ClaimWorkerConnection(ctx, oldCredential, oldIncarnation, connectionOwner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.ExecContext(ctx, `UPDATE direct_workers SET transport_enabled=true,bootstrap_deployment_id='old-deployment' WHERE id=$1`, worker.ID); err != nil {
		t.Fatal(err)
	}
	if targets, listErr := store.offlineRunningWorkers(ctx); listErr != nil || len(targets) != 0 {
		t.Fatalf("healthy connected worker selected for replacement: %v targets=%v", listErr, targets)
	}
	a, err := store.assignment(ctx, principal.AccountID, boxID)
	if err != nil {
		t.Fatal(err)
	}
	state, replacementEnrollment, started, err := store.beginWorkerReplacement(ctx, principal.AccountID, a, "new-deployment")
	if err != nil || !started || state.Worker.Epoch != oldWorker.Epoch+1 {
		t.Fatalf("begin replacement: %+v started=%v err=%v", state, started, err)
	}
	if targets, listErr := store.offlineRunningWorkers(ctx); listErr != nil || len(targets) != 1 || targets[0].accountID != principal.AccountID || targets[0].boxID != boxID {
		t.Fatalf("offline running worker missing from replacement scan: %v targets=%v", listErr, targets)
	}
	if _, err = store.AuthenticateWorker(ctx, oldCredential); !errors.Is(err, errWorkerIdentity) {
		t.Fatal("old replacement credential retained authority", err)
	}
	if _, err = store.ClaimWorkerConnection(ctx, oldCredential, oldIncarnation, connectionOwner); !errors.Is(err, errWorkerIdentity) {
		t.Fatal("old replacement credential reclaimed authority", err)
	}
	pending, duplicateToken, duplicate, err := store.beginWorkerReplacement(ctx, principal.AccountID, a, "new-deployment")
	if err != nil || duplicate || duplicateToken != "" || pending.Worker.Epoch != state.Worker.Epoch {
		t.Fatalf("pending replacement rotated: %+v token=%q started=%v err=%v", pending, duplicateToken, duplicate, err)
	}
	newCredential, _ := secretToken()
	if _, err = store.ExchangeWorkerEnrollment(ctx, replacementEnrollment, newCredential); err != nil {
		t.Fatal(err)
	}
	newIncarnation, _ := secretToken()
	newWorker, err := store.ClaimWorkerConnection(ctx, newCredential, newIncarnation, connectionOwner)
	if err != nil {
		t.Fatal(err)
	}
	if targets, listErr := store.offlineRunningWorkers(ctx); listErr != nil || len(targets) != 0 {
		t.Fatalf("reconnected worker still selected for replacement: %v targets=%v", listErr, targets)
	}
	wrong := a
	wrong.FencingToken = "wrong-fence"
	if err = store.finishWorkerReplacement(ctx, newWorker, wrong, "new-deployment"); !errors.Is(err, errWorkerIdentity) {
		t.Fatal("replacement finalized across assignment fence", err)
	}
	if err = store.finishWorkerReplacement(ctx, newWorker, a, "new-deployment"); err != nil {
		t.Fatal(err)
	}
	var bootstrap string
	var target sql.NullString
	var enabled bool
	if err = store.DB.QueryRowContext(ctx, `SELECT bootstrap_deployment_id,replacement_deployment_instance_id,transport_enabled FROM direct_workers WHERE id=$1`, worker.ID).Scan(&bootstrap, &target, &enabled); err != nil || bootstrap != "new-deployment" || target.Valid || !enabled {
		t.Fatalf("replacement final state bootstrap=%q target=%v enabled=%v err=%v", bootstrap, target, enabled, err)
	}
	for index, credentialed := range []bool{false, true} {
		name := "enrollment"
		if credentialed {
			name = "credential"
		}
		t.Run("pending-initial-"+name, func(t *testing.T) {
			pendingSlot, pendingBox := uuid(), uuid()
			service := "pending-" + name + "-service"
			fence := "pending-" + name + "-fence"
			if _, err = store.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,ordinal,state,service_id,assignment_generation,fencing_token) VALUES($1,$2,'railway',$3,'occupied',$4,1,$5)`, pendingSlot, principal.AccountID, index+2, service, fence); err != nil {
				t.Fatal(err)
			}
			if _, err = store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,slot_id,assignment_generation,fencing_token) VALUES($1,$2,$3,$4,'railway','attaching',$5,$5,$6,1,$7)`, pendingBox, principal.AccountID, principal.UserID, "pending-"+name+"-box", "pending-"+name+"-volume", pendingSlot, fence); err != nil {
				t.Fatal(err)
			}
			pendingWorker, oldEnrollment, err := store.IssueWorkerEnrollment(ctx, principal.AccountID, pendingSlot)
			if err != nil {
				t.Fatal(err)
			}
			oldCredential := ""
			if credentialed {
				oldCredential, _ = secretToken()
				if _, err = store.ExchangeWorkerEnrollment(ctx, oldEnrollment, oldCredential); err != nil {
					t.Fatal(err)
				}
			}
			pendingAssignment, err := store.assignment(ctx, principal.AccountID, pendingBox)
			if err != nil {
				t.Fatal(err)
			}
			baseline, _ := json.Marshal(v1.SessionInventory{Assignment: nativeFence(pendingAssignment), State: "live", Sessions: []v1.Session{}})
			if _, err = store.DB.ExecContext(ctx, `UPDATE direct_workers SET migration_sessions=$1::jsonb,bootstrap_deployment_id='old-pending-deployment' WHERE id=$2`, baseline, pendingWorker.ID); err != nil {
				t.Fatal(err)
			}
			pending := pendingWorkerRecovery{Worker: pendingWorker, Baseline: baseline, BootstrapDeployment: "old-pending-deployment", Enrollment: !credentialed, Credential: credentialed}
			freshEnrollment, err := store.rotatePendingInitialEnrollment(ctx, principal.AccountID, pendingAssignment, pending, "fresh-pending-deployment")
			if err != nil || !validWorkerSecret(freshEnrollment) {
				t.Fatal("rotate pending initial authority", err)
			}
			if _, err = store.rotatePendingInitialEnrollment(ctx, principal.AccountID, pendingAssignment, pending, "another-deployment"); !errors.Is(err, errWorkerIdentity) {
				t.Fatal("stale pending epoch rotated authority", err)
			}
			if credentialed {
				if _, err = store.AuthenticateWorker(ctx, oldCredential); !errors.Is(err, errWorkerIdentity) {
					t.Fatal("old pending credential retained authority", err)
				}
			} else {
				candidate, _ := secretToken()
				if _, err = store.ExchangeWorkerEnrollment(ctx, oldEnrollment, candidate); !errors.Is(err, errWorkerIdentity) {
					t.Fatal("old pending enrollment retained authority", err)
				}
			}
			freshCredential, _ := secretToken()
			if _, err = store.ExchangeWorkerEnrollment(ctx, freshEnrollment, freshCredential); err != nil {
				t.Fatal("fresh pending enrollment did not exchange", err)
			}
			var deployment string
			var epoch int64
			if err = store.DB.QueryRowContext(ctx, `SELECT bootstrap_deployment_id,connection_epoch FROM direct_workers WHERE id=$1`, pendingWorker.ID).Scan(&deployment, &epoch); err != nil || deployment != "fresh-pending-deployment" || epoch != pendingWorker.Epoch+1 {
				t.Fatalf("pending rotation state deployment=%q epoch=%d err=%v", deployment, epoch, err)
			}
		})
	}
	if _, err = store.DB.ExecContext(ctx, `UPDATE direct_workers SET bootstrap_deployment_id=NULL WHERE id=$1`, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, started, err = store.beginWorkerReplacement(ctx, principal.AccountID, a, "third-deployment"); err == nil || started {
		t.Fatal("replacement accepted absent installed-deployment proof", err)
	}
}
