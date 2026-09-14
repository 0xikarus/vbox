package controller

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/transport"
	"github.com/0xikarus/vmbox-service/internal/workeragent"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDirectWorkerEnrollmentAndConnectionPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "direct_worker_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = admin.DB.ExecContext(ctx, "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	defer admin.DB.ExecContext(context.Background(), "DROP SCHEMA "+name+" CASCADE")
	dbConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	dbConfig.RuntimeParams["search_path"] = name
	s := &Store{DB: stdlib.OpenDB(*dbConfig)}
	defer s.Close()
	if err = s.DB.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	s.DB.SetMaxOpenConns(4)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	ownerToken := uuid()
	p, err := s.Bootstrap(ctx, "disposable-direct-worker", "owner", ownerToken)
	if err != nil {
		t.Fatal(err)
	}
	slot := uuid()
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,ordinal,state,service_id) VALUES($1,$2,'railway',1,'free','disposable-service')`, slot, p.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.IssueWorkerEnrollment(ctx, uuid(), slot); err == nil {
		t.Fatal("cross-account enrollment accepted")
	}
	worker, token, err := s.IssueWorkerEnrollment(ctx, p.AccountID, slot)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.IssueWorkerEnrollment(ctx, p.AccountID, slot); err == nil {
		t.Fatal("existing enrollment displaced")
	}
	credential, _ := secretToken()
	enrolled, err := s.ExchangeWorkerEnrollment(ctx, token, credential)
	if err != nil || enrolled.ID != worker.ID {
		t.Fatalf("enrollment %+v %v", enrolled, err)
	}
	if _, err = s.ExchangeWorkerEnrollment(ctx, token, credential); !errors.Is(err, errWorkerIdentity) {
		t.Fatalf("reused enrollment: %v", err)
	}
	if _, err = s.Authenticate(ctx, credential); err == nil {
		t.Fatal("worker credential granted controller access")
	}
	incarnation, _ := secretToken()
	owner, _ := secretToken()

	if err = s.ClaimWorkerController(ctx, owner); err != nil {
		t.Fatal(err)
	}
	otherOwner, _ := secretToken()
	if err = s.ClaimWorkerController(ctx, otherOwner); err == nil {
		t.Fatal("second active controller accepted")
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE direct_worker_controller_lease SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimWorkerController(ctx, otherOwner); err != nil {
		t.Fatal("expired controller lease blocked failover", err)
	}
	if _, err = s.DB.ExecContext(ctx, `DELETE FROM direct_worker_controller_lease`); err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimWorkerConnection(ctx, credential, incarnation, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RenewWorkerConnection(ctx, first, owner, []byte(`{"health":"ok"}`)); err != nil {
		t.Fatal(err)
	}
	second, err := s.ClaimWorkerConnection(ctx, credential, incarnation, owner)
	if err != nil {
		t.Fatal(err)
	}
	if second.Epoch <= first.Epoch {
		t.Fatal("connection epoch did not advance")
	}
	if err = s.RenewWorkerConnection(ctx, first, owner, []byte(`{}`)); !errors.Is(err, errWorkerIdentity) {
		t.Fatalf("stale connection renewed: %v", err)
	}
	if err = s.ReleaseWorkerConnection(ctx, first, owner); err != nil {
		t.Fatal(err)
	}
	if err = s.RenewWorkerConnection(ctx, second, owner, []byte(`{}`)); err != nil {
		t.Fatal("stale release killed new connection", err)
	}

	// Run a real TLS agent/controller connection. The fake Railway provider is
	// absent: executing through this connection cannot consult its API or SSH.
	server := NewServer(s, nil)
	tls := httptest.NewTLSServer(server.Handler())
	defer tls.Close()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	bindingPath := filepath.Join(dir, "binding.json")
	binding := workerprotocol.Binding{AccountID: p.AccountID, SlotID: slot, BoxID: uuid(), Assignment: "disposable-assignment", Incarnation: incarnation}
	if _, err = s.DB.ExecContext(ctx, `UPDATE compute_slots SET assignment_generation=1,fencing_token='test-fence',state='occupied' WHERE id=$1`, slot); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,slot_id,assignment_generation,fencing_token) VALUES($1,$2,$3,'disposable-box','railway','running','disposable-volume','disposable-volume',$4,1,'test-fence')`, binding.BoxID, p.AccountID, p.UserID, slot); err != nil {
		t.Fatal(err)
	}
	assigned, err := s.assignment(ctx, p.AccountID, binding.BoxID)
	if err != nil {
		t.Fatal(err)
	}
	binding.Assignment = nativeFence(assigned)
	bindingJSON, _ := json.Marshal(binding)
	if err = os.WriteFile(bindingPath, bindingJSON, 0600); err != nil {
		t.Fatal(err)
	}
	config := workeragent.Config{ControllerURL: tls.URL, AccountID: p.AccountID, SlotID: slot, WorkerID: worker.ID, Credential: credential, JournalDirectory: filepath.Join(dir, "journal"), BindingFile: bindingPath}
	configJSON, _ := json.Marshal(config)
	if err = os.WriteFile(configPath, configJSON, 0600); err != nil {
		t.Fatal(err)
	}
	connected := make(chan struct{}, 1)
	agentCtx, stopAgent := context.WithCancel(ctx)
	defer stopAgent()
	agent := workeragent.Agent{Config: config, ConfigFile: configPath, HTTP: tls.Client(), Incarnation: incarnation, Report: func(message string) {
		if message == "worker connected" {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	}}
	agentDone := make(chan error, 1)
	go func() { agentDone <- agent.Run(agentCtx) }()
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("agent failed to connect")
	}
	var active *directWorkerConnection
	for active == nil {
		server.mu.Lock()
		active = server.directWorkers[worker.ID]
		server.mu.Unlock()
		if active == nil {
			select {
			case <-ctx.Done():
				t.Fatal("controller failed to register peer")
			case <-time.After(time.Millisecond):
			}
		}
	}
	// Runtime evidence arrives over the real TLS peer, not a Railway read.
	var receivedObservation []byte
	var observedAt time.Time
	for {
		err = s.DB.QueryRowContext(ctx, `SELECT observation,observed_at FROM direct_workers WHERE id=$1 AND observation_epoch=$2 AND observation_sequence>0`, worker.ID, active.Worker.Epoch).Scan(&receivedObservation, &observedAt)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("agent observation not recorded")
		case <-time.After(10 * time.Millisecond):
		}
	}
	var observed workerprotocol.Observation
	if json.Unmarshal(receivedObservation, &observed) != nil || observed.Binding != binding {
		t.Fatal("recorded worker observation has wrong binding")
	}
	server.mu.Lock()
	connectionOwner := server.directWorkerOwner
	server.mu.Unlock()
	if err = s.RenewWorkerConnection(ctx, active.Worker, connectionOwner, nil); err != nil {
		t.Fatal(err)
	}
	var afterHeartbeat time.Time
	if err = s.DB.QueryRowContext(ctx, `SELECT observed_at FROM direct_workers WHERE id=$1`, worker.ID).Scan(&afterHeartbeat); err != nil {
		t.Fatal(err)
	}
	if !observedAt.Equal(afterHeartbeat) {
		t.Fatal("heartbeat made old runtime evidence fresh")
	}
	wrongEpoch := active.Worker
	wrongEpoch.Epoch--
	observed.Sequence++
	if err = s.RecordWorkerObservation(ctx, wrongEpoch, connectionOwner, observed); err == nil {
		t.Fatal("old connection epoch recorded runtime evidence")
	}

	stream, err := active.Peer.Open(ctx, workerprotocol.Request{OperationID: uuid(), Binding: binding, Argv: []string{"sh", "-c", "printf direct-worker-ok; exit 7"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	code, err := workerprotocol.ReadOutput(stream, &output, nil)
	stream.Close()
	if err != nil || code != 7 || output.String() != "direct-worker-ok" {
		t.Fatalf("direct execution %d %q %v", code, output.String(), err)
	}

	// Advance only this disposable database assignment. Connection resolution
	// must synchronize the real TLS agent without any provider/SSH operation.
	previousBinding := binding
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE compute_slots SET assignment_generation=2,fencing_token='next-test-fence' WHERE id=$1`, slot); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE logical_boxes SET assignment_generation=2,fencing_token='next-test-fence' WHERE id=$1`, binding.BoxID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assigned, err = s.assignment(ctx, p.AccountID, binding.BoxID)
	if err != nil {
		t.Fatal(err)
	}
	binding.Assignment = nativeFence(assigned)

	if _, err = s.DB.ExecContext(ctx, `UPDATE direct_workers SET transport_enabled=true WHERE id=$1`, worker.ID); err != nil {
		t.Fatal(err)
	}
	server.DirectWorkersEnabled = true
	forbidden := &noRailwayCallsProvider{}
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return forbidden, nil }
	wrapped, err := server.provider(ctx, p.AccountID, "railway", "")
	if err != nil {
		t.Fatal(err)
	}
	// Disabling new enrollment/activation cannot silently demote an already
	// enabled worker to the backing Railway transport.
	server.DirectWorkersEnabled = false
	wrappedWithRolloutDisabled, err := server.provider(ctx, p.AccountID, "railway", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := wrappedWithRolloutDisabled.(*directWorkerProvider); !ok {
		t.Fatal("enabled worker bypassed direct routing when rollout was disabled")
	}
	if rolloutConn, err := wrappedWithRolloutDisabled.Connection(ctx, "disposable-service"); err != nil || rolloutConn.Transport != directWorkerTransport {
		t.Fatalf("enabled worker did not retain direct routing: %+v %v", rolloutConn, err)
	}
	server.DirectWorkersEnabled = true
	conn, err := wrapped.Connection(ctx, "disposable-service")
	if err != nil {
		t.Fatal(err)
	}
	if conn.Transport != directWorkerTransport || !connectionMatchesAssignment(conn, assigned) {
		t.Fatalf("bad direct connection %+v", conn)
	}
	appliedData, err := os.ReadFile(bindingPath)
	if err != nil {
		t.Fatal(err)
	}
	var applied workerprotocol.Binding
	if json.Unmarshal(appliedData, &applied) != nil || applied != binding {
		t.Fatal("controller did not synchronize agent assignment")
	}
	staleStream, err := active.Peer.Open(ctx, workerprotocol.Request{OperationID: uuid(), Binding: previousBinding, Argv: []string{"printf", "stale-must-not-execute"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = staleStream.CloseWrite()
	if _, err = workerprotocol.ReadOutput(staleStream, nil, nil); err == nil {
		t.Fatal("old assignment accepted after synchronization")
	}
	staleStream.Close()
	// sudo is a test fixture: this exercises transport/argv rather than host UID isolation.
	sudo := filepath.Join(dir, "sudo")
	if err = os.WriteFile(sudo, []byte("#!/bin/sh\n[ \"$1\" = -n ] && [ \"$2\" = -H ] && [ \"$3\" = -u ] && [ \"$4\" = vmbox ] && [ \"$5\" = -- ] || exit 97\nshift 5\nexec \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	result, err := wrapped.Exec(ctx, "disposable-service", []string{"sh", "-c", "printf wrapped-worker-ok; exit 7"}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 7 || result.Stdout != "wrapped-worker-ok" {
		t.Fatalf("wrapped execution %+v %v", result, err)
	}
	// Simulate an already-installed runtime's fingerprint response inside the
	// disposable agent. This checks bootstrap routing without changing host tools.
	sudoFixture, err := os.ReadFile(sudo)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapFixture := `#!/bin/sh
[ "$1" = -n ] && [ "$2" = -H ] && [ "$3" = -u ] && [ "$4" = vmbox ] && [ "$5" = -- ] || exit 97
shift 5
[ "$1" = env ] && [ "$7" = sudo ] && [ "$8" = -n ] && [ "$9" = -- ] || exit 98
for argument do fingerprint="$argument"; done
printf 'vmbox-bootstrap-ready:%s\n' "$fingerprint"
`
	if err = os.WriteFile(sudo, []byte(bootstrapFixture), 0700); err != nil {
		t.Fatal(err)
	}
	if err = wrapped.(provider.Bootstrapper).Bootstrap(ctx, "disposable-service", provider.BootstrapRequest{}); err != nil {
		t.Fatalf("bootstrap over direct worker: %v", err)
	}
	if err = os.WriteFile(sudo, sudoFixture, 0700); err != nil {
		t.Fatal(err)
	}
	baselineInventory := v1.SessionInventory{Assignment: binding.Assignment, State: "live", Sessions: []v1.Session{{ID: "$1", Name: "surviving-session", Incarnation: binding.Assignment + ":" + strings.Repeat("b", 24) + ":$1"}}}
	baselineData, _ := json.Marshal(baselineInventory)
	if _, err = s.DB.ExecContext(ctx, `UPDATE direct_workers SET transport_enabled=false,migration_sessions=$1::jsonb,bootstrap_deployment_id='disposable-test-deployment' WHERE id=$2`, baselineData, worker.ID); err != nil {
		t.Fatal(err)
	}
	activation := func() int {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, tls.URL+"/v1/worker-slots/"+slot+"/activate", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+ownerToken)
		response, err := tls.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return response.StatusCode
	}
	// Runtime inventory fixtures verify the transition without launching or
	// rebinding a host tmux server. A missing survivor must leave legacy selected.
	missing := baselineInventory
	missing.Sessions = nil
	missingData, _ := json.Marshal(missing)
	writeInventory := func(data []byte) {
		if err := os.WriteFile(sudo, []byte("#!/bin/sh\ncat <<'INVENTORY'\n"+string(data)+"\nINVENTORY\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeInventory(missingData)
	if status := activation(); status != 409 {
		t.Fatalf("missing session activation status %d", status)
	}
	var enabled bool
	if err := s.DB.QueryRowContext(ctx, `SELECT transport_enabled FROM direct_workers WHERE id=$1`, worker.ID).Scan(&enabled); err != nil || enabled {
		t.Fatal("missing session enabled transport", err)
	}
	writeInventory(baselineData)
	if status := activation(); status != 200 {
		t.Fatalf("verified activation status %d", status)
	}
	if err = os.WriteFile(sudo, sudoFixture, 0700); err != nil {
		t.Fatal(err)
	}
	staleWorker := active.Worker
	staleWorker.Epoch--
	if err := s.ActivateVerifiedWorker(ctx, staleWorker, assigned, baselineData); err == nil {
		t.Fatal("stale connection activated")
	}
	client := transport.Worker{ControllerURL: tls.URL, Token: ownerToken, HTTP: tls.Client()}
	result, err = client.ExecConnection(ctx, conn, []string{"sh", "-c", "printf client-worker-ok; printf client-error >&2; exit 7"}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 7 || result.Stdout != "client-worker-ok" || result.Stderr != "client-error" {
		t.Fatalf("client/controller/worker execution: %+v %v", result, err)
	}
	unauthorized := client
	unauthorized.Token = credential
	if _, err = unauthorized.ExecConnection(ctx, conn, []string{"true"}, provider.ExecOptions{}); err == nil {
		t.Fatal("worker credential granted client execution")
	}
	staleConn := conn
	staleConn.Metadata = make(map[string]string)
	for key, value := range conn.Metadata {
		staleConn.Metadata[key] = value
	}
	staleConn.Metadata["connectionRevision"] = "stale-incarnation"
	if _, err = client.ExecConnection(ctx, staleConn, []string{"true"}, provider.ExecOptions{}); err == nil {
		t.Fatal("client stale incarnation accepted")
	}
	// Binary transfer exceeds the protocol window and must preserve bytes without
	// retaining a second copy in the provider result.
	payload := strings.Repeat("\x00\xffworker-stream\n", 65536)
	var transferred bytes.Buffer
	result, err = client.StreamConnection(ctx, conn, []string{"cat"}, provider.ExecOptions{Stdin: strings.NewReader(payload), Stdout: &transferred})
	if err != nil || result.ExitCode != 0 || transferred.String() != payload || result.Stdout != "" {
		t.Fatalf("direct binary stream: exit=%d bytes=%d err=%v", result.ExitCode, transferred.Len(), err)
	}
	// A changed database assignment must terminate an already-running stream,
	// even while the original agent connection stays healthy.
	started := make(workerStartedWriter, 1)
	interrupted := make(chan error, 1)
	go func() {
		_, err := client.StreamConnection(ctx, conn, []string{"sh", "-c", "printf started; exec sleep 30"}, provider.ExecOptions{Stdout: started})
		interrupted <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("stream did not start")
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE compute_slots SET fencing_token='replacement-fence' WHERE id=$1`, slot); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-interrupted:
		if err == nil {
			t.Fatal("revoked stream reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("assignment change did not terminate stream")
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE compute_slots SET fencing_token='next-test-fence' WHERE id=$1`, slot); err != nil {
		t.Fatal(err)
	}
	if _, err = wrapped.(provider.ConnectionExecutor).ExecConnection(ctx, provider.Connection{Transport: directWorkerTransport, Endpoint: conn.Endpoint, Metadata: map[string]string{"accountId": "other"}}, []string{"true"}, provider.ExecOptions{}); err == nil {
		t.Fatal("stale connection accepted")
	}
	// Release only the disposable database assignment; no provider resource or
	// user worker is stopped. Maintenance uses a distinct, fenced slot identity.
	if err = s.ReleaseAssignment(ctx, p.AccountID, binding.BoxID, 2, "next-test-fence", v1.LogicalBoxHibernated); err != nil {
		t.Fatal(err)
	}
	freeConn, err := wrapped.Connection(ctx, "disposable-service")
	if err != nil {
		t.Fatalf("free worker connection: %v", err)
	}
	if freeConn.Metadata["boxId"] != "compute-slot:"+slot || freeConn.Metadata["assignment"] == binding.Assignment {
		t.Fatal("free slot reused logical-box authority")
	}
	maintenance, err := wrapped.Exec(ctx, "disposable-service", []string{"printf", "maintenance-ok"}, provider.ExecOptions{})
	if err != nil || maintenance.Stdout != "maintenance-ok" {
		t.Fatalf("free-slot maintenance: %+v %v", maintenance, err)
	}
	if _, err = client.ExecConnection(ctx, freeConn, []string{"true"}, provider.ExecOptions{}); err == nil {
		t.Fatal("owner logical-box endpoint accepted maintenance identity")
	}
	if _, err = wrapped.(provider.ConnectionExecutor).ExecConnection(ctx, conn, []string{"true"}, provider.ExecOptions{}); err == nil {
		t.Fatal("released box connection remained valid")
	}

	// A free-looking slot with a leftover fence must fail closed.
	if _, err = s.DB.ExecContext(ctx, `UPDATE compute_slots SET fencing_token='unconfirmed' WHERE id=$1`, slot); err != nil {
		t.Fatal(err)
	}
	if _, err = wrapped.Connection(ctx, "disposable-service"); err == nil {
		t.Fatal("ambiguous free slot authorized maintenance")
	}
	tx, err = s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE compute_slots SET state='occupied',assignment_generation=3,fencing_token='third-test-fence' WHERE id=$1`, slot); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE logical_boxes SET state='running',slot_id=$2,assignment_generation=3,fencing_token='third-test-fence' WHERE id=$1`, binding.BoxID, slot); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	resumed, err := wrapped.Connection(ctx, "disposable-service")
	if err != nil {
		t.Fatalf("free-to-assigned transition: %v", err)
	}
	if resumed.Metadata["boxId"] != binding.BoxID || resumed.Metadata["assignment"] == freeConn.Metadata["assignment"] {
		t.Fatal("new assignment did not replace maintenance identity")
	}
	if _, err = wrapped.(provider.ConnectionExecutor).ExecConnection(ctx, freeConn, []string{"true"}, provider.ExecOptions{}); err == nil {
		t.Fatal("old maintenance connection survived reservation")
	}
	stopAgent()
	select {
	case <-agentDone:
	case <-ctx.Done():
		t.Fatal("agent did not stop")
	}
	if _, err = wrapped.Connection(ctx, "disposable-service"); err == nil {
		t.Fatal("offline agent silently fell back")
	}
	server.DirectWorkersEnabled = false
	disabled, err := server.provider(ctx, p.AccountID, "railway", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = disabled.Connection(ctx, "disposable-service"); err == nil {
		t.Fatal("offline enrolled agent fell back after rollout was disabled")
	}
	server.DirectWorkersEnabled = true
	if err = wrapped.(provider.Bootstrapper).Bootstrap(ctx, "disposable-service", provider.BootstrapRequest{}); err == nil {
		t.Fatal("offline bootstrap silently fell back")
	}
	// The direct connection claimed a later epoch; refresh it for revocation.
	second, err = s.ClaimWorkerConnection(ctx, credential, incarnation, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeWorker(ctx, uuid(), worker.ID); !errors.Is(err, errWorkerIdentity) {
		t.Fatalf("cross-account revoke: %v", err)
	}
	if err = s.RevokeWorker(ctx, p.AccountID, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateWorker(ctx, credential); !errors.Is(err, errWorkerIdentity) {
		t.Fatalf("revoked credential accepted: %v", err)
	}
	if err = s.RenewWorkerConnection(ctx, second, owner, []byte(`{}`)); !errors.Is(err, errWorkerIdentity) {
		t.Fatalf("revoked connection renewed: %v", err)
	}
	// Exercise the owner bootstrap route using an explicitly disposable second
	// slot. The transport fixture captures stdin instead of installing on the host.
	installSlot, installBox := uuid(), uuid()
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,ordinal,state,service_id,deployment_instance_id,assignment_generation,fencing_token) VALUES($1,$2,'railway',2,'occupied','install-service','install-deployment',1,'install-fence')`, installSlot, p.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,slot_id,assignment_generation,fencing_token) VALUES($1,$2,$3,'install-box','railway','running','install-volume','install-volume',$4,1,'install-fence')`, installBox, p.AccountID, p.UserID, installSlot); err != nil {
		t.Fatal(err)
	}
	installer := &workerInstallFixture{}
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return installer, nil }
	server.WorkerAgent = []byte("disposable-binary-fixture")
	server.WorkerRuntime = []byte("disposable-runtime-fixture")
	server.PublicURL = tls.URL
	installRequest := func() (int, []byte) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, tls.URL+"/v1/worker-slots/"+installSlot+"/enrollment", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+ownerToken)
		response, err := tls.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, body
	}
	server.PublicURL = "http://invalid-controller.example"
	status, _ := installRequest()
	if status != 409 || installer.calls != 0 {
		t.Fatal("invalid configuration attempted worker installation")
	}
	var enrollmentCount int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM direct_workers WHERE slot_id=$1`, installSlot).Scan(&enrollmentCount); err != nil || enrollmentCount != 0 {
		t.Fatal("unused enrollment was not cleaned up", err)
	}
	server.PublicURL = tls.URL
	status, response := installRequest()
	if status != 202 || bytes.Contains(response, []byte("enrollmentToken")) || bytes.Contains(response, []byte("credential")) {
		t.Fatalf("installation returned status %d or exposed credential fields", status)
	}
	var pending bool
	if err := s.DB.QueryRowContext(ctx, `SELECT enrollment_hash IS NOT NULL AND credential_hash IS NULL AND NOT transport_enabled FROM direct_workers WHERE slot_id=$1`, installSlot).Scan(&pending); err != nil || !pending {
		t.Fatal("installation prematurely activated worker", err)
	}
	status, _ = installRequest()
	if status != 409 || installer.calls != 1 {
		t.Fatal("duplicate enrollment repeated installation")
	}

	var enrollmentHash, preservedBaseline []byte
	if err = s.DB.QueryRowContext(ctx, `SELECT enrollment_hash,migration_sessions FROM direct_workers WHERE slot_id=$1`, installSlot).Scan(&enrollmentHash, &preservedBaseline); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE direct_workers SET enrollment_expires_at=now()-interval '1 minute' WHERE slot_id=$1`, installSlot); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='attaching' WHERE id=$1`, installBox); err != nil {
		t.Fatal(err)
	}
	recoverRequest := func() int {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, tls.URL+"/v1/worker-slots/"+installSlot+"/recover", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+ownerToken)
		response, err := tls.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if bytes.Contains(body, []byte("enrollmentToken")) || bytes.Contains(body, []byte("credential")) {
			t.Fatal("recovery response exposed secret fields")
		}
		return response.StatusCode
	}
	if status := recoverRequest(); status != 202 || installer.calls != 2 {
		t.Fatalf("configured recovery status=%d calls=%d", status, installer.calls)
	}
	var afterHash, afterBaseline []byte
	var renewed bool
	if err = s.DB.QueryRowContext(ctx, `SELECT enrollment_hash,migration_sessions,enrollment_expires_at>now() FROM direct_workers WHERE slot_id=$1`, installSlot).Scan(&afterHash, &afterBaseline, &renewed); err != nil {
		t.Fatal(err)
	}
	if !renewed || !bytes.Equal(enrollmentHash, afterHash) || !bytes.Equal(preservedBaseline, afterBaseline) {
		t.Fatal("recovery changed token or session baseline")
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE direct_workers SET migration_sessions=NULL WHERE slot_id=$1`, installSlot); err != nil {
		t.Fatal(err)
	}
	if status := recoverRequest(); status != 409 || installer.calls != 2 {
		t.Fatal("recovery accepted a missing original baseline")
	}
	// Complete the same preserved first-time enrollment through the automatic
	// readiness helper while the logical box is still attaching. The fixture
	// starts a real agent from the private payload captured by the one legacy
	// bootstrap; no enrollment token is returned by an HTTP response.
	if _, err = s.DB.ExecContext(ctx, `UPDATE direct_workers SET migration_sessions=$1::jsonb WHERE slot_id=$2`, preservedBaseline, installSlot); err != nil {
		t.Fatal(err)
	}
	installedConfig, installedBinding, err := decodeWorkerInstallationFixture(installer.payload)
	if err != nil {
		t.Fatal(err)
	}
	autoDir := t.TempDir()
	autoConfigPath := filepath.Join(autoDir, "config.json")
	autoBindingPath := filepath.Join(autoDir, "binding.json")
	installedConfig.JournalDirectory = filepath.Join(autoDir, "journal")
	installedConfig.BindingFile = autoBindingPath
	if err = os.WriteFile(autoBindingPath, installedBinding, 0600); err != nil {
		t.Fatal(err)
	}
	autoSudoFixture := "#!/bin/sh\ncase \"$*\" in\n  *'/usr/local/bin/vmbox-runtime put-file'*) sha256sum | cut -d ' ' -f 1; exit 0 ;;\n  *vmbox-install-runtime*) printf 'ok\\n'; exit 0 ;;\n  *worker-agent-native-sessions-empty*) printf '" + emptyNativeSessionMarker + "\\n'; exit 0 ;;\nesac\ncat <<'INVENTORY'\n" + string(preservedBaseline) + "\nINVENTORY\n"
	if err = os.WriteFile(sudo, []byte(autoSudoFixture), 0700); err != nil {
		t.Fatal(err)
	}
	autoIncarnation, _ := secretToken()
	autoConnected := make(chan struct{}, 1)
	autoCtx, stopAuto := context.WithCancel(ctx)
	autoAgent := workeragent.Agent{Config: installedConfig, ConfigFile: autoConfigPath, HTTP: tls.Client(), Incarnation: autoIncarnation, Report: func(message string) {
		if message == "worker connected" {
			select {
			case autoConnected <- struct{}{}:
			default:
			}
		}
	}}
	autoDone := make(chan error, 1)
	go func() { autoDone <- autoAgent.Run(autoCtx) }()
	defer stopAuto()
	select {
	case <-autoConnected:
	case <-ctx.Done():
		t.Fatal("new worker agent did not connect")
	}
	installAssignment, err := s.assignment(ctx, p.AccountID, installBox)
	if err != nil {
		t.Fatal(err)
	}
	installConnection, err := installer.Connection(ctx, "install-service")
	if err != nil {
		t.Fatal(err)
	}
	wrappedInstall, err := server.provider(ctx, p.AccountID, "railway", "")
	if err != nil {
		t.Fatal(err)
	}
	bootstrapCalls := installer.calls
	if err = server.ensureAutomaticWorkerTransport(ctx, p.AccountID, installAssignment, wrappedInstall, installConnection); err != nil {
		t.Fatal("automatic first-time activation failed", err)
	}
	if installer.calls != bootstrapCalls {
		t.Fatalf("attaching activation used backing Railway after installation: calls=%d before=%d", installer.calls, bootstrapCalls)
	}
	var automaticallyEnabled bool
	var bootstrapDeployment string
	if err = s.DB.QueryRowContext(ctx, `SELECT transport_enabled,bootstrap_deployment_id FROM direct_workers WHERE slot_id=$1`, installSlot).Scan(&automaticallyEnabled, &bootstrapDeployment); err != nil || !automaticallyEnabled || bootstrapDeployment != "install-deployment" {
		t.Fatal("new worker was not enabled on its exact bootstrap deployment", err)
	}
	// Once the one pinned installation has completed, every workspace-runtime
	// setup command for this first attaching assignment must resolve through the
	// live agent. Replacing the backing provider with a panic fixture makes any
	// accidental Railway runtime fallback fail the test immediately.
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) {
		return &noRailwayCallsProvider{}, nil
	}
	directSetup, err := server.provider(ctx, p.AccountID, "railway", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"/usr/local/bin/vmbox-runtime", "put-file", stagedRuntimePath, "0600"},
		{"vmbox-runtime", "health"},
		{"vmbox-runtime", "restore-tools"},
		{"vmbox-runtime", "tmux-restore"},
		{"vmbox-runtime", "tmux-context", "new-box", "slot", "running", "connected"},
		{"vmbox-runtime", "native-bind", nativeFence(installAssignment)},
	} {
		if _, err := directSetup.Exec(ctx, "install-service", argv, provider.ExecOptions{}); err != nil {
			t.Fatalf("post-install runtime command %v did not use the direct worker: %v", argv, err)
		}
	}
	stopAuto()
	select {
	case <-autoDone:
	case <-ctx.Done():
		t.Fatal("new worker agent did not stop")
	}
}

type workerInstallFixture struct {
	noRailwayCallsProvider
	calls   int
	payload []byte
}

func (p *workerInstallFixture) Connection(_ context.Context, id string) (provider.Connection, error) {
	if id != "install-service" {
		return provider.Connection{}, errors.New("unexpected bootstrap connection target")
	}
	return provider.Connection{Transport: "openssh", Endpoint: "install-deployment@ssh.railway.com", Metadata: map[string]string{"deploymentInstanceId": "install-deployment"}}, nil
}

func (p *workerInstallFixture) ExecConnection(ctx context.Context, conn provider.Connection, argv []string, options provider.ExecOptions) (provider.ExecResult, error) {
	if conn.Metadata["deploymentInstanceId"] != "install-deployment" {
		return provider.ExecResult{}, errors.New("unexpected bootstrap deployment")
	}
	return p.Exec(ctx, "install-service", argv, options)
}

func (p *workerInstallFixture) Exec(_ context.Context, id string, argv []string, options provider.ExecOptions) (provider.ExecResult, error) {
	if id == "install-service" && len(argv) == 3 && argv[0] == "vmbox-runtime" && argv[1] == "native-sessions" {
		data, _ := json.Marshal(v1.SessionInventory{Assignment: argv[2], State: "live", Sessions: []v1.Session{}})
		return provider.ExecResult{Stdout: string(data)}, nil
	}
	p.calls++
	if id != "install-service" || strings.Join(argv, " ") != "sudo -n sh -s" || options.Stdin == nil {
		return provider.ExecResult{}, errors.New("unexpected bootstrap command")
	}
	data, err := io.ReadAll(io.LimitReader(options.Stdin, 50*1024*1024))
	if err == nil && bytes.Contains(data, []byte("VMBOX_RECOVERY_IDENTITY")) {
		if !bytes.Contains(data, []byte("--verify-installation")) {
			return provider.ExecResult{}, errors.New("recovery verification missing")
		}
		return provider.ExecResult{Stdout: "worker-agent-installation-recovery-started\n"}, nil
	}
	if err != nil || !bytes.Contains(data, []byte("VMBOX_WORKER_PAYLOAD")) {
		return provider.ExecResult{}, errors.New("bootstrap payload missing")
	}
	p.payload = append([]byte(nil), data...)
	return provider.ExecResult{Stdout: "worker-agent-installed\n"}, nil
}

func decodeWorkerInstallationFixture(payload []byte) (workeragent.Config, []byte, error) {
	var config workeragent.Config
	_, encoded, ok := strings.Cut(string(payload), "<<'VMBOX_WORKER_PAYLOAD'\n")
	if !ok {
		return config, nil, errors.New("installation archive missing")
	}
	encoded, _, ok = strings.Cut(encoded, "\nVMBOX_WORKER_PAYLOAD\n")
	if !ok {
		return config, nil, errors.New("installation archive terminator missing")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return config, nil, err
	}
	reader := tar.NewReader(bytes.NewReader(data))
	var binding []byte
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return config, nil, nextErr
		}
		content, readErr := io.ReadAll(io.LimitReader(reader, 32*1024))
		if readErr != nil {
			return config, nil, readErr
		}
		switch header.Name {
		case "config.json":
			if json.Unmarshal(content, &config) != nil {
				return config, nil, errors.New("invalid installed config")
			}
		case "binding.json":
			binding = append([]byte(nil), content...)
		}
	}
	if config.EnrollmentToken == "" || len(binding) == 0 {
		return config, nil, errors.New("private enrollment assets missing")
	}
	return config, binding, nil
}

// Any call to the backing management provider fails this integration test.
type noRailwayCallsProvider struct{ provider.Provider }

func (*noRailwayCallsProvider) Name() string { return "railway" }
func (*noRailwayCallsProvider) Exec(context.Context, string, []string, provider.ExecOptions) (provider.ExecResult, error) {
	panic("unexpected Railway execution")
}
func (*noRailwayCallsProvider) Connection(context.Context, string) (provider.Connection, error) {
	panic("unexpected Railway connection lookup")
}
func (*noRailwayCallsProvider) Inspect(context.Context, string) (provider.Box, error) {
	panic("unexpected Railway inspection")
}
func (*noRailwayCallsProvider) Bootstrap(context.Context, string, provider.BootstrapRequest) error {
	panic("unexpected Railway bootstrap")
}

type workerStartedWriter chan struct{}

func (w workerStartedWriter) Write(data []byte) (int, error) {
	select {
	case w <- struct{}{}:
	default:
	}
	return len(data), nil
}
