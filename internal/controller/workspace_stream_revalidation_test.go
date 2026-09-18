package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// fakeWorkerAgent speaks the same worker-protocol handshake and multiplexed
// stream as internal/workeragent without executing commands. Accepted streams
// emulate a long-running vmbox-runtime desktop-stream process by emitting VNC
// frames until the stream or the agent connection ends.
type fakeWorkerAgent struct {
	peer    *workerprotocol.Peer
	welcome workerprotocol.Welcome
	ended   chan error
}

func dialFakeWorkerAgent(t *testing.T, tlsURL, credential, incarnation string, binding workerprotocol.Binding, client *http.Client) *fakeWorkerAgent {
	t.Helper()
	ctx := t.Context()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	endpoint := strings.Replace(tlsURL, "https://", "wss://", 1) + "/v1/workers/connect"
	conn, _, err := websocket.Dial(dialCtx, endpoint, &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + credential}},
	})
	if err != nil {
		t.Fatalf("agent dial: %v", err)
	}
	hello := workerprotocol.Hello{Version: workerprotocol.Version, Incarnation: incarnation, Capabilities: []string{"exec-v1", "stream-v1", "journal-v1", "binding-v1", "observations-v1"}, Binding: &binding}
	if err := wsjson.Write(dialCtx, conn, hello); err != nil {
		t.Fatalf("agent hello: %v", err)
	}
	agent := &fakeWorkerAgent{ended: make(chan error, 1)}
	if err := wsjson.Read(dialCtx, conn, &agent.welcome); err != nil {
		t.Fatalf("agent welcome: %v", err)
	}
	agent.peer = workerprotocol.New(ctx, conn, false)
	go func() {
		for {
			stream, err := agent.peer.Accept(ctx)
			if err != nil {
				agent.ended <- err
				return
			}
			if stream.Request.Rebind != nil {
				go acknowledgeRebind(stream)
				continue
			}
			go emulateDesktopStream(ctx, stream)
		}
	}()
	return agent
}

// acknowledgeRebind mirrors the real agent's binding acknowledgement.
func acknowledgeRebind(stream *workerprotocol.Stream) {
	code := 0
	encoder := json.NewEncoder(stream)
	if err := encoder.Encode(workerprotocol.Output{Kind: "exit", ExitCode: &code}); err != nil {
		stream.Close()
		return
	}
	_ = stream.CloseWrite()
}

func (a *fakeWorkerAgent) stop(t *testing.T, server *Server, mock sqlmock.Sqlmock) {
	t.Helper()
	a.peer.Close()
	select {
	case <-a.ended:
	case <-time.After(5 * time.Second):
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		registered := server.directWorkers[a.welcome.WorkerID]
		server.mu.Unlock()
		if registered == nil {
			// The deferred release runs right after deregistration; give it a
			// moment, then stop waiting even when queued reads remain unused.
			time.Sleep(100 * time.Millisecond)
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("controller did not release the worker connection")
}

func emulateDesktopStream(ctx context.Context, stream *workerprotocol.Stream) {
	defer stream.Close()
	// Frames are JSON-wrapped exactly like the real vmbox-runtime desktop-stream
	// process, whose stdout is transported by the worker protocol encoder.
	encoder := json.NewEncoder(stream)
	frame := func(i int) bool {
		return encoder.Encode(workerprotocol.Output{Kind: "stdout", Data: []byte("desktop-frame-" + strings.Repeat("f", 24) + "-" + fmt.Sprint(i))}) == nil
	}
	if !frame(0) {
		return
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := stream.Read(buf); err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for i := 1; ; i++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !frame(i) {
				return
			}
		}
	}
}

func authWorkerRows(worker DirectWorker) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "account_id", "slot_id", "service_id", "incarnation", "epoch", "transport_enabled"}).
		AddRow(worker.ID, worker.AccountID, worker.SlotID, "svc", worker.Incarnation, worker.Epoch, worker.Enabled)
}

func workerIdentityRows(worker DirectWorker, live bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "account_id", "slot_id", "service_id", "incarnation", "epoch", "transport_enabled", "live"}).
		AddRow(worker.ID, worker.AccountID, worker.SlotID, "svc", worker.Incarnation, worker.Epoch, worker.Enabled, live)
}

// The periodic connection renewal runs every 20 seconds with a five-second
// deadline. One stalled renewal is a transient store failure: the agent
// connection and its live viewer streams must survive it.
func TestWorkerConnectionRenewalSurvivesTransientStoreFailure(t *testing.T) {
	store, mock := testStore(t)
	server := NewServer(store, nil)
	server.DirectWorkersEnabled = true
	tls := httptest.NewTLSServer(server.Handler())
	defer tls.Close()

	credential, _ := secretToken()
	incarnation, _ := secretToken()
	worker := DirectWorker{ID: "worker-1", AccountID: "account-1", SlotID: "slot-1", Enabled: true, Incarnation: incarnation, Epoch: 7}
	binding := workerprotocol.Binding{AccountID: worker.AccountID, SlotID: worker.SlotID, BoxID: "box-1", Assignment: testFence(), Incarnation: incarnation}

	expectAgentHandshake(mock, credential, worker)
	mock.ExpectExec(`UPDATE direct_workers SET connection_expires_at=NULL`).WillReturnResult(sqlmock.NewResult(0, 1))
	agent := dialFakeWorkerAgent(t, tls.URL, credential, incarnation, binding, tls.Client())
	active := waitRegistered(t, server, worker.ID)

	stream, err := active.Peer.Open(t.Context(), workerprotocol.Request{OperationID: "op-1", Binding: binding, Argv: []string{"vmbox-runtime", "desktop-stream", binding.Assignment}})
	if err != nil {
		t.Fatalf("open viewer stream: %v", err)
	}
	frames := make(chan string, 16)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stream.Read(buf)
			if err != nil {
				return
			}
			select {
			case frames <- string(buf[:n]):
			default:
			}
		}
	}()
	select {
	case frame := <-frames:
		if !strings.Contains(frame, `"kind":"stdout"`) {
			t.Fatalf("unexpected frame %q", frame)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("viewer stream produced no frames")
	}

	// The first renewal tick (20s) claims the controller lease; the store stalls
	// past the five-second deadline. Nothing about the assignment changed.
	mock.ExpectQuery(`INSERT INTO direct_worker_controller_lease`).WillDelayFor(6 * time.Second).WillReturnError(errors.New("statement timeout"))

	// Wait past the failed renewal (and the point where the previous behavior
	// closed this connection) before judging it.
	time.Sleep(26500 * time.Millisecond)
	select {
	case err := <-agent.ended:
		t.Fatalf("agent connection ended after a transient renewal failure: %v", err)
	default:
	}
	select {
	case _, ok := <-frames:
		if !ok {
			t.Fatal("viewer stream was torn down by a transient renewal failure")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("viewer stream stopped carrying frames after a transient renewal failure")
	}
	agent.stop(t, server, mock)
}

// expectWorkerBinding queues the store reads behind one authoritative binding
// lookup whose assignment fence matches testFence.
func expectWorkerBinding(mock sqlmock.Sqlmock, worker DirectWorker) {
	mock.ExpectQuery(`SELECT b.id::text FROM logical_boxes b JOIN compute_slots c ON c.id=b.slot_id`).
		WithArgs(worker.AccountID, worker.SlotID).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("box-1"))
	now := time.Now().UTC()
	mock.ExpectQuery(`FROM logical_boxes\s+WHERE account_id=\$1 AND id=\$2`).WithArgs(worker.AccountID, "box-1").WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "owner_user_id", "name", "provider", "provider_credential", "default_agent", "role", "state", "volume_id", "volume_name", "slot_id", "assignment_generation", "lease_owner", "lease_expires_at", "restoration_state", "failure_reason", "created_at", "updated_at", "tools"}).
		AddRow("box-1", worker.AccountID, "user-1", "box", "railway", "primary", "shell", "worker", "running", "vol", "vol", worker.SlotID, 1, "", nil, "", "", now, now, "[]"))
	mock.ExpectQuery(`SELECT COALESCE\(fencing_token,''\) FROM logical_boxes`).WithArgs(worker.AccountID, "box-1").WillReturnRows(sqlmock.NewRows([]string{"fencing_token"}).AddRow("fence-token"))
	mock.ExpectQuery(`FROM compute_slots s LEFT JOIN logical_boxes b ON b.slot_id=s.id\s+WHERE s.account_id=\$1 AND s.id=\$2`).WithArgs(worker.AccountID, worker.SlotID).WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "provider", "provider_credential", "ordinal", "state", "service_id", "service_name", "deployment_instance_id", "box_id", "box_name", "region", "image", "image_version", "health", "assignment_generation", "lease_owner", "lease_expires_at", "failure_reason", "created_at", "updated_at"}).
		AddRow(worker.SlotID, worker.AccountID, "railway", "primary", 1, "occupied", "svc", "", "", "box-1", "box", "", "", "", "healthy", 1, "", nil, "", now, now))
}

// testFence mirrors nativeFence for the fixed worker binding row inputs.
func testFence() string {
	sum := sha256.Sum256([]byte("box-1:1:fence-token"))
	return fmt.Sprintf("%x", sum)
}

func expectAgentHandshake(mock sqlmock.Sqlmock, credential string, worker DirectWorker) {
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(`FROM direct_workers w JOIN compute_slots c ON c.id=w.slot_id AND c.account_id=w.account_id\s+WHERE w.credential_hash`).
		WithArgs(secrets.TokenHash(credential)).WillReturnRows(authWorkerRows(worker))
	mock.ExpectQuery(`INSERT INTO direct_worker_controller_lease`).WillReturnRows(sqlmock.NewRows([]string{"owner"}).AddRow("owner-token"))
	mock.ExpectQuery(`UPDATE direct_workers w SET incarnation`).WillReturnRows(authWorkerRows(worker))
}

func waitRegistered(t *testing.T, server *Server, workerID string) *directWorkerConnection {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		active := server.directWorkers[workerID]
		server.mu.Unlock()
		if active != nil {
			return active
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("controller did not register the agent connection")
	return nil
}

// One transient store failure while the agent connection is healthy must not
// tear down that connection or the live viewer streams it carries.
func TestWorkerConnectionSurvivesTransientStoreFailure(t *testing.T) {
	store, mock := testStore(t)
	server := NewServer(store, nil)
	server.DirectWorkersEnabled = true
	tls := httptest.NewTLSServer(server.Handler())
	defer tls.Close()

	credential, _ := secretToken()
	incarnation, _ := secretToken()
	worker := DirectWorker{ID: "worker-1", AccountID: "account-1", SlotID: "slot-1", Enabled: true, Incarnation: incarnation, Epoch: 7}
	binding := workerprotocol.Binding{AccountID: worker.AccountID, SlotID: worker.SlotID, BoxID: "box-1", Assignment: testFence(), Incarnation: incarnation}

	expectAgentHandshake(mock, credential, worker)
	// The handler releases its connection lease whenever it eventually returns.
	mock.ExpectExec(`UPDATE direct_workers SET connection_expires_at=NULL`).WillReturnResult(sqlmock.NewResult(0, 1))
	agent := dialFakeWorkerAgent(t, tls.URL, credential, incarnation, binding, tls.Client())
	if agent.welcome.Epoch != 7 || agent.welcome.WorkerID != worker.ID {
		t.Fatalf("welcome=%+v", agent.welcome)
	}
	active := waitRegistered(t, server, worker.ID)

	stream, err := active.Peer.Open(t.Context(), workerprotocol.Request{OperationID: "op-1", Binding: binding, Argv: []string{"vmbox-runtime", "desktop-stream", binding.Assignment}})
	if err != nil {
		t.Fatalf("open viewer stream: %v", err)
	}
	frames := make(chan string, 16)
	readsDone := make(chan struct{})
	go func() {
		defer close(readsDone)
		buf := make([]byte, 4096)
		for {
			n, err := stream.Read(buf)
			if err != nil {
				return
			}
			select {
			case frames <- string(buf[:n]):
			default:
			}
		}
	}()
	select {
	case frame := <-frames:
		if !strings.Contains(frame, `"kind":"stdout"`) {
			t.Fatalf("unexpected frame %q", frame)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("viewer stream produced no frames")
	}

	// A routine observation write stalls past its deadline: a transient store
	// failure on a healthy connection. Nothing about the assignment changed.
	expectWorkerBinding(mock, worker)
	mock.ExpectExec(`UPDATE direct_workers SET observation`).WillDelayFor(6 * time.Second).WillReturnError(errors.New("statement timeout"))
	if err := agent.peer.PublishObservation(workerprotocol.Observation{Sequence: 1, Binding: binding}); err != nil {
		t.Fatalf("publish observation: %v", err)
	}

	// Wait past the failed write before judging the connection.
	time.Sleep(6500 * time.Millisecond)

	// The stream must still be alive long after the failed write, and the agent
	// connection must not have ended.
	select {
	case err := <-agent.ended:
		t.Fatalf("agent connection ended after a transient store failure: %v", err)
	default:
	}
	select {
	case frame, ok := <-frames:
		if !ok {
			t.Fatal("viewer stream was torn down by a transient store failure")
		}
		if !strings.Contains(frame, `"kind":"stdout"`) {
			t.Fatalf("unexpected frame %q", frame)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("viewer stream stopped carrying frames after a transient store failure")
	}
	agent.stop(t, server, mock)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The per-second stream revalidation must survive one transient store error and
// still fail closed immediately when the assignment fence really changes.
func TestStreamRevalidationToleratesTransientStoreErrorButNotReassignment(t *testing.T) {
	store, mock := testStore(t)
	server := NewServer(store, nil)
	server.DirectWorkersEnabled = true
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) {
		return &noRailwayCallsProvider{}, nil
	}
	tls := httptest.NewTLSServer(server.Handler())
	defer tls.Close()

	credential, _ := secretToken()
	incarnation, _ := secretToken()
	worker := DirectWorker{ID: "worker-1", AccountID: "account-1", SlotID: "slot-1", Enabled: true, Incarnation: incarnation, Epoch: 7}
	binding := workerprotocol.Binding{AccountID: worker.AccountID, SlotID: worker.SlotID, BoxID: "box-1", Assignment: testFence(), Incarnation: incarnation}

	expectAgentHandshake(mock, credential, worker)
	mock.ExpectExec(`UPDATE direct_workers SET connection_expires_at=NULL`).WillReturnResult(sqlmock.NewResult(0, 1))
	agent := dialFakeWorkerAgent(t, tls.URL, credential, incarnation, binding, tls.Client())
	waitRegistered(t, server, worker.ID)

	// Each revalidation reads the worker identity, the authoritative binding,
	// and then the same pair again. Queue healthy reads for the initial
	// connection and stream setup, one transient DirectWorkerForService failure
	// mid-stream, and a fencing-token replacement that must end the stream.
	const prelude = 4
	mock.ExpectQuery(`WHERE w.account_id=\$1 AND c.provider=\$2`).WithArgs(worker.AccountID, "railway", "", "svc").WillReturnRows(workerIdentityRows(worker, true))
	for range 3 {
		expectBindingReads(mock, worker, "box-1", "fence-token")
	}
	for range 4 * prelude {
		mock.ExpectQuery(`WHERE w.account_id=\$1 AND c.provider=\$2`).WithArgs(worker.AccountID, "railway", "", "svc").WillReturnRows(workerIdentityRows(worker, true))
	}
	mock.ExpectQuery(`WHERE w.account_id=\$1 AND c.provider=\$2`).WithArgs(worker.AccountID, "railway", "", "svc").WillReturnError(errors.New("connection reset by peer"))
	for range 40 {
		mock.ExpectQuery(`WHERE w.account_id=\$1 AND c.provider=\$2`).WithArgs(worker.AccountID, "railway", "", "svc").WillReturnRows(workerIdentityRows(worker, true))
	}
	// Binding reads: healthy fence until the replacement, then the new fence.
	expectRepeatingBindingReads(mock, worker, "box-1", "fence-token", 2*prelude+4)
	expectRepeatingBindingReads(mock, worker, "box-1", "replacement-fence", 60)

	prov, err := server.provider(t.Context(), worker.AccountID, "railway", "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := prov.Connection(t.Context(), "svc")
	if err != nil {
		t.Fatalf("connection resolution: %v", err)
	}
	if conn.Transport != directWorkerTransport {
		t.Fatalf("transport=%q", conn.Transport)
	}
	streamer, ok := prov.(provider.ConnectionStreamer)
	if !ok {
		t.Fatal("direct worker provider lost stream support")
	}
	streamCtx, stopStream := context.WithTimeout(t.Context(), 20*time.Second)
	defer stopStream()
	var output lastFrameWriter
	streamDone := make(chan error, 1)
	go func() {
		_, err := streamer.StreamConnection(streamCtx, conn, []string{"vmbox-runtime", "desktop-stream", binding.Assignment}, provider.ExecOptions{Stdout: &output})
		streamDone <- err
	}()

	// Frames must keep flowing across the transient failure window.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if output.frames() >= 3 && time.Since(output.lastWrite()) < 1500*time.Millisecond {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if output.frames() < 3 {
		t.Fatalf("stream stopped producing frames after the transient store error: frames=%d", output.frames())
	}
	select {
	case err := <-streamDone:
		t.Fatalf("stream ended during the transient store failure window: %v", err)
	default:
	}

	// The fencing-token replacement is definitive: the stream must end.
	select {
	case err := <-streamDone:
		if err == nil {
			t.Fatal("reassigned stream reported success")
		}
	case <-time.After(12 * time.Second):
		t.Fatal("assignment change did not terminate the stream")
	}
	agent.stop(t, server, mock)
	_ = mock
}

// expectBindingReads queues one authoritative binding lookup with the given fence.
func expectBindingReads(mock sqlmock.Sqlmock, worker DirectWorker, boxID, fence string) {
	mock.ExpectQuery(`SELECT b.id::text FROM logical_boxes b JOIN compute_slots c ON c.id=b.slot_id`).
		WithArgs(worker.AccountID, worker.SlotID).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(boxID))
	now := time.Now().UTC()
	mock.ExpectQuery(`FROM logical_boxes\s+WHERE account_id=\$1 AND id=\$2`).WithArgs(worker.AccountID, boxID).WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "owner_user_id", "name", "provider", "provider_credential", "default_agent", "role", "state", "volume_id", "volume_name", "slot_id", "assignment_generation", "lease_owner", "lease_expires_at", "restoration_state", "failure_reason", "created_at", "updated_at", "tools"}).
		AddRow(boxID, worker.AccountID, "user-1", "box", "railway", "primary", "shell", "worker", "running", "vol", "vol", worker.SlotID, 1, "", nil, "", "", now, now, "[]"))
	mock.ExpectQuery(`SELECT COALESCE\(fencing_token,''\) FROM logical_boxes`).WithArgs(worker.AccountID, boxID).WillReturnRows(sqlmock.NewRows([]string{"fencing_token"}).AddRow(fence))
	mock.ExpectQuery(`FROM compute_slots s LEFT JOIN logical_boxes b ON b.slot_id=s.id\s+WHERE s.account_id=\$1 AND s.id=\$2`).WithArgs(worker.AccountID, worker.SlotID).WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "provider", "provider_credential", "ordinal", "state", "service_id", "service_name", "deployment_instance_id", "box_id", "box_name", "region", "image", "image_version", "health", "assignment_generation", "lease_owner", "lease_expires_at", "failure_reason", "created_at", "updated_at"}).
		AddRow(worker.SlotID, worker.AccountID, "railway", "primary", 1, "occupied", "svc", "", "", boxID, "box", "", "", "", "healthy", 1, "", nil, "", now, now))
}

func expectRepeatingBindingReads(mock sqlmock.Sqlmock, worker DirectWorker, boxID, fence string, count int) {
	for range count {
		expectBindingReads(mock, worker, boxID, fence)
	}
}

// lastFrameWriter records viewer frames as the desktop stream pumps them.
type lastFrameWriter struct {
	mu     sync.Mutex
	count  int
	lastAt time.Time
}

func (w *lastFrameWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.count++
	w.lastAt = time.Now()
	return len(data), nil
}
func (w *lastFrameWriter) frames() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}
func (w *lastFrameWriter) lastWrite() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastAt
}

var _ = testing.Short

func TestDefinitiveWorkerErrorClassification(t *testing.T) {
	definitive := []error{
		errWorkerAgentReconnecting,
		errWorkerConnectionUnavailable,
		errWorkerOffline,
		errWorkerOwnershipChanged,
		errWorkerAssignmentChanged,
		errWorkerAssignmentScope,
		errWorkerAckUnavailable,
		errWorkerIdentity,
		fmt.Errorf("wrapped: %w", errWorkerAssignmentChanged),
	}
	for _, err := range definitive {
		if !definitiveWorkerError(err) {
			t.Fatalf("definitive error not classified: %v", err)
		}
	}
	// errControllerLeaseLost is connection-scoped policy, not a stream-level
	// fence, so it is not part of definitiveWorkerError.
	if definitiveWorkerError(errControllerLeaseLost) {
		t.Fatal("controller lease loss should not be a stream-level classification")
	}
	for _, err := range []error{errWorkerIdentity, errControllerLeaseLost} {
		if !lostWorkerLease(err) {
			t.Fatalf("lease loss not classified: %v", err)
		}
	}
	for _, err := range []error{errWorkerAgentReconnecting, errWorkerOffline, errors.New("connection reset by peer"), nil} {
		if lostWorkerLease(err) {
			t.Fatalf("non-lease error classified as lease loss: %v", err)
		}
	}
	transient := []error{
		errors.New("connection reset by peer"),
		context.DeadlineExceeded,
		errors.New("driver: bad connection"),
		nil,
	}
	for _, err := range transient {
		if definitiveWorkerError(err) {
			t.Fatalf("transient error classified as definitive: %v", err)
		}
	}
}

func TestWorkspaceStreamRevalidationPolicy(t *testing.T) {
	assignmentRows := func(mock sqlmock.Sqlmock, fence, state string) {
		now := time.Now().UTC()
		mock.ExpectQuery(`FROM logical_boxes\s+WHERE account_id=\$1 AND id=\$2`).WithArgs("account-1", "box-1").WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "owner_user_id", "name", "provider", "provider_credential", "default_agent", "role", "state", "volume_id", "volume_name", "slot_id", "assignment_generation", "lease_owner", "lease_expires_at", "restoration_state", "failure_reason", "created_at", "updated_at", "tools"}).
			AddRow("box-1", "account-1", "user-1", "box", "railway", "primary", "shell", "worker", state, "vol", "vol", "slot-1", 1, "", nil, "", "", now, now, "[]"))
		mock.ExpectQuery(`SELECT COALESCE\(fencing_token,''\) FROM logical_boxes`).WithArgs("account-1", "box-1").WillReturnRows(sqlmock.NewRows([]string{"fencing_token"}).AddRow(fence))
		mock.ExpectQuery(`FROM compute_slots s LEFT JOIN logical_boxes b ON b.slot_id=s.id\s+WHERE s.account_id=\$1 AND s.id=\$2`).WithArgs("account-1", "slot-1").WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "provider", "provider_credential", "ordinal", "state", "service_id", "service_name", "deployment_instance_id", "box_id", "box_name", "region", "image", "image_version", "health", "assignment_generation", "lease_owner", "lease_expires_at", "failure_reason", "created_at", "updated_at"}).
			AddRow("slot-1", "account-1", "railway", "primary", 1, "occupied", "svc", "", "", "box-1", "box", "", "", "", "healthy", 1, "", nil, "", now, now))
	}
	authenticateRows := func(mock sqlmock.Sqlmock, role string) {
		mock.ExpectQuery(`SELECT t.account_id::text,t.user_id::text,u.role,u.subject FROM access_tokens`).WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"account_id", "user_id", "role", "subject"}).AddRow("account-1", "user-1", role, "person"))
	}
	principal := Principal{AccountID: "account-1", UserID: "user-1", Role: "owner"}
	request := httptest.NewRequest(http.MethodGet, "/v1/logical-boxes/box-1/desktop/stream", nil)
	request.Header.Set("Authorization", "Bearer session-token")

	t.Run("healthy viewer stays alive", func(t *testing.T) {
		store, mock := testStore(t)
		server := &Server{Store: store, PublicURL: "https://controller.example"}
		assignmentRows(mock, "fence-token", "running")
		authenticateRows(mock, "owner")
		var flaky time.Time
		a := fleetAssignment{Box: v1.LogicalBox{ID: "box-1"}}
		if !server.workspaceStreamAlive(t.Context(), request, principal, testFence(), a, &flaky) {
			t.Fatal("healthy revalidation ended the stream")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("transient store failure keeps the viewer inside the grace window", func(t *testing.T) {
		store, mock := testStore(t)
		server := &Server{Store: store, PublicURL: "https://controller.example"}
		mock.ExpectQuery(`FROM logical_boxes\s+WHERE account_id=\$1 AND id=\$2`).WillReturnError(errors.New("driver: bad connection"))
		var flaky time.Time
		a := fleetAssignment{Box: v1.LogicalBox{ID: "box-1"}}
		if !server.workspaceStreamAlive(t.Context(), request, principal, testFence(), a, &flaky) {
			t.Fatal("first transient store failure ended the stream")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("persistent store failure fails closed after the grace window", func(t *testing.T) {
		store, mock := testStore(t)
		server := &Server{Store: store, PublicURL: "https://controller.example"}
		mock.ExpectQuery(`FROM logical_boxes\s+WHERE account_id=\$1 AND id=\$2`).WillReturnError(errors.New("driver: bad connection"))
		flaky := time.Now().Add(-streamRevalidationGrace - time.Second)
		a := fleetAssignment{Box: v1.LogicalBox{ID: "box-1"}}
		if server.workspaceStreamAlive(t.Context(), request, principal, testFence(), a, &flaky) {
			t.Fatal("grace-expired transient failure kept the stream")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	for name, setup := range map[string]func(mock sqlmock.Sqlmock){
		"assignment fence change": func(mock sqlmock.Sqlmock) { assignmentRows(mock, "replacement-fence", "running") },
		"stopped box":             func(mock sqlmock.Sqlmock) { assignmentRows(mock, "fence-token", "hibernated") },
		"revoked token": func(mock sqlmock.Sqlmock) {
			assignmentRows(mock, "fence-token", "running")
			mock.ExpectQuery(`SELECT t.account_id::text,t.user_id::text,u.role,u.subject FROM access_tokens`).WithArgs(sqlmock.AnyArg()).WillReturnError(errInvalidBearerToken)
		},
		"role downgrade": func(mock sqlmock.Sqlmock) {
			assignmentRows(mock, "fence-token", "running")
			authenticateRows(mock, "user")
		},
		"deleted box": func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(`FROM logical_boxes\s+WHERE account_id=\$1 AND id=\$2`).WillReturnError(errLogicalBoxMissing)
		},
	} {
		t.Run("definitive: "+name, func(t *testing.T) {
			store, mock := testStore(t)
			server := &Server{Store: store, PublicURL: "https://controller.example"}
			setup(mock)
			var flaky time.Time
			a := fleetAssignment{Box: v1.LogicalBox{ID: "box-1"}}
			if server.workspaceStreamAlive(t.Context(), request, principal, testFence(), a, &flaky) {
				t.Fatal("definitive revalidation failure kept the stream")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
