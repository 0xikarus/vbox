package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/events"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

type fakeProvider struct {
	boxes   []provider.Box
	created provider.CreateRequest
	argv    []string
	deleted provider.Owner
}

type taskRuntimeProvider struct {
	fakeProvider
	calls  [][]string
	digest string
}

func (p *taskRuntimeProvider) Exec(_ context.Context, _ string, argv []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	p.calls = append(p.calls, append([]string(nil), argv...))
	switch len(p.calls) {
	case 1:
		return provider.ExecResult{Stdout: p.digest + "\n"}, nil
	case 2:
		return provider.ExecResult{Stdout: "ok\n"}, nil
	default:
		return provider.ExecResult{}, nil
	}
}

func (*fakeProvider) Name() string { return "fake" }
func (*fakeProvider) Validate(context.Context) (provider.Capabilities, error) {
	return provider.Capabilities{Provider: "fake", ExactArgv: true}, nil
}
func (p *fakeProvider) Create(_ context.Context, req provider.CreateRequest) (provider.Box, error) {
	p.created = req
	return provider.Box{ID: "box-provider-id", Name: req.Name, Owner: req.Owner, State: provider.StateRunning}, nil
}
func (*fakeProvider) Inspect(context.Context, string) (provider.Box, error) {
	return provider.Box{ID: "box-provider-id", State: provider.StateRunning}, nil
}
func (p *fakeProvider) List(context.Context) ([]provider.Box, error) {
	return append([]provider.Box(nil), p.boxes...), nil
}
func (*fakeProvider) Start(context.Context, string) (provider.Box, error) {
	return provider.Box{}, nil
}
func (*fakeProvider) Stop(context.Context, string) (provider.Box, error) { return provider.Box{}, nil }
func (*fakeProvider) Resize(context.Context, string, provider.Resources) (provider.Box, error) {
	return provider.Box{}, nil
}
func (p *fakeProvider) Delete(_ context.Context, _ string, owner provider.Owner) error {
	p.deleted = owner
	return nil
}
func (*fakeProvider) CreateStorage(context.Context, string, provider.Resources) (provider.Storage, error) {
	return provider.Storage{}, nil
}
func (*fakeProvider) AttachStorage(context.Context, string, provider.Storage) error { return nil }
func (*fakeProvider) DeleteStorage(context.Context, provider.Storage, provider.Owner) error {
	return nil
}
func (*fakeProvider) Deploy(context.Context, string, string) (provider.Box, error) {
	return provider.Box{}, nil
}
func (*fakeProvider) Connection(context.Context, string) (provider.Connection, error) {
	return provider.Connection{}, nil
}
func (*fakeProvider) Logs(context.Context, string, provider.LogOptions, io.Writer) error { return nil }
func (*fakeProvider) Usage(context.Context, string) (provider.Usage, error) {
	return provider.Usage{}, nil
}
func (p *fakeProvider) Exec(_ context.Context, _ string, argv []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	p.argv = append([]string(nil), argv...)
	return provider.ExecResult{}, nil
}
func (*fakeProvider) Reconcile(_ context.Context, box provider.Box) (provider.Box, error) {
	return box, nil
}

func TestBoxTaskStagesMatchingRuntimeBeforeStartingAgent(t *testing.T) {
	runtime := []byte("current controller runtime")
	for agent, command := range map[string]string{"claude": "chat-deliver", "codex": "chat-codex-start", "opencode": ""} {
		t.Run(agent, func(t *testing.T) {
			p := &taskRuntimeProvider{digest: fmt.Sprintf("%x", sha256.Sum256(runtime))}
			server := NewServer(nil, nil)
			server.WorkerRuntime = runtime
			task := v1.BoxTask{Session: "fresh-" + agent, Agent: agent}
			message := v1.BoxMessage{ID: "message-1", Text: "hello"}
			if _, err := server.startBoxTaskRuntime(context.Background(), "account-1", p, "service-1", task, message); err != nil {
				t.Fatal(err)
			}
			wantCalls := 4
			if agent == "codex" || agent == "opencode" {
				wantCalls = 3
			}
			if len(p.calls) != wantCalls {
				t.Fatalf("calls=%v", p.calls)
			}
			if got := p.calls[0]; len(got) < 2 || got[0] != "/usr/local/bin/vmbox-runtime" || got[1] != "put-file" {
				t.Fatalf("first call did not stage the runtime: %v", got)
			}
			if agent == "codex" {
				if got := p.calls[2]; len(got) != 3 || got[0] != "vmbox-runtime" || got[1] != command || got[2] != task.Session {
					t.Fatalf("Codex initial message did not use process startup: %v", got)
				}
				return
			}
			if agent == "opencode" {
				got := p.calls[2]
				if len(got) != 6 || got[0] != "vmbox-runtime" || got[1] != "tmux-task" || got[2] != task.Session || got[3] != task.Agent || got[4] != message.ID {
					t.Fatalf("OpenCode initial message was not passed at process startup: %v", got)
				}
				prompt, err := base64.RawURLEncoding.DecodeString(got[5])
				if err != nil || !strings.Contains(string(prompt), message.Text) {
					t.Fatalf("OpenCode startup prompt=%q err=%v", prompt, err)
				}
				return
			}
			if got := p.calls[2]; len(got) != 6 || got[0] != "vmbox-runtime" || got[1] != "tmux-task" || got[2] != task.Session || got[3] != task.Agent || got[4] != message.ID || got[5] != "" {
				t.Fatalf("task started before matching runtime was installed: %v", got)
			}
			if got := p.calls[3]; len(got) != 3 || got[0] != "vmbox-runtime" || got[1] != command || got[2] != task.Session {
				t.Fatalf("initial agent message did not use the native conversation path: %v", got)
			}
		})
	}
}

func TestShellTaskStillReceivesItsInitialTerminalCommand(t *testing.T) {
	runtime := []byte("current controller runtime")
	p := &taskRuntimeProvider{digest: fmt.Sprintf("%x", sha256.Sum256(runtime))}
	server := NewServer(nil, nil)
	server.WorkerRuntime = runtime
	task := v1.BoxTask{Session: "fresh-shell", Agent: "shell"}
	message := v1.BoxMessage{ID: "message-1", Text: "printf hello"}
	if _, err := server.startBoxTaskRuntime(context.Background(), "account-1", p, "service-1", task, message); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 3 || len(p.calls[2]) != 6 || p.calls[2][1] != "tmux-task" || p.calls[2][5] == "" {
		t.Fatalf("shell command was not delivered through its terminal: %v", p.calls)
	}
}

func TestTerminalInputIdempotencyIsScopedToTmuxSession(t *testing.T) {
	first := terminalInputMessageID("account-a", "box-a", "codex-one", "same-key")
	second := terminalInputMessageID("account-a", "box-a", "codex-two", "same-key")
	if first == second {
		t.Fatalf("terminal input IDs collided across sessions: %s", first)
	}
	if first != terminalInputMessageID("account-a", "box-a", "codex-one", "same-key") {
		t.Fatal("terminal input ID is not stable within one session")
	}
}

func TestOwnerEndpointsRejectUserRole(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`SELECT t.account_id::text`).WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"account_id", "user_id", "role", "subject"}).AddRow("account-a", "user-a", "user", "person"))
	server := NewServer(store, provider.NewRegistry())
	req := httptest.NewRequest(http.MethodGet, "/v1/users", nil)
	req.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLogicalBoxConnectionEndpointRequiresOwner(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`SELECT t.account_id::text`).WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"account_id", "user_id", "role", "subject"}).AddRow("account-a", "user-a", "user", "person"))
	server := NewServer(store, provider.NewRegistry())
	request := httptest.NewRequest(http.MethodGet, "/v1/logical-boxes/box-a/connection", nil)
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAPIGetRunCannotCrossTenant(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`SELECT t.account_id::text`).WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"account_id", "user_id", "role", "subject"}).AddRow("account-a", "user-a", "owner", "person"))
	mock.ExpectQuery(`SELECT id::text,account_id::text`).WithArgs("account-a", "run-in-account-b").WillReturnRows(sqlmock.NewRows(runColumns()))
	server := NewServer(store, provider.NewRegistry())
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/run-in-account-b", nil)
	req.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleUsesAccountCredentialAndPreservesExactArgv(t *testing.T) {
	store, mock := testStore(t)
	for _, state := range []v1.JobState{v1.JobProvisioning, v1.JobPreparing, v1.JobRunning} {
		mock.ExpectExec(`UPDATE runs SET state=`).WithArgs("account-a", "run-1", state, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	providerFake := &fakeProvider{}
	server := NewServer(store, provider.NewRegistry())
	var delivered []string
	server.Deliver = func(_ context.Context, _ v1.Run, event string, _ v1.JobState, _ string, _ string) {
		delivered = append(delivered, event)
	}
	bootstrapped := false
	server.Bootstrap = func(_ context.Context, selected provider.Provider, box provider.Box, components []string) error {
		bootstrapped = true
		if selected != providerFake || box.ID != "box-provider-id" || string(mustJSON(t, components)) != string(mustJSON(t, []string{"codex", "claude"})) {
			t.Fatalf("bootstrap provider=%T box=%+v components=%v", selected, box, components)
		}
		return nil
	}
	var resolvedAccount, resolvedName string
	server.Resolve = func(_ context.Context, account, name, credential string) (provider.Provider, error) {
		resolvedAccount, resolvedName = account, credential
		return providerFake, nil
	}
	argv := []string{"tool", "--flag", "two words", "$HOME;"}
	run := v1.Run{ID: "run-1", AccountID: "account-a", Provider: "fake", Lease: "lease-1", Request: v1.CreateRunRequest{Provider: "fake", ProviderCredential: "primary", Box: "worker", Command: argv, Components: []string{"codex", "claude"}}}
	server.schedule(context.Background(), Principal{AccountID: "account-a"}, run)
	if resolvedAccount != "account-a" || resolvedName != "primary" {
		t.Fatalf("resolved account=%q credential=%q", resolvedAccount, resolvedName)
	}
	if string(mustJSON(t, providerFake.argv)) != string(mustJSON(t, argv)) {
		t.Fatalf("argv=%#v", providerFake.argv)
	}
	if providerFake.created.Env["VMBOX_RUN_ID"] != "run-1" || providerFake.created.Env["VMBOX_EVENT_KEY"] != "lease-1" {
		t.Fatalf("runtime identity env=%v", providerFake.created.Env)
	}
	if !bootstrapped {
		t.Fatal("controller forwarded the command before bootstrapping the workload")
	}
	if string(mustJSON(t, delivered)) != string(mustJSON(t, []string{"provisioning", "ready"})) {
		t.Fatalf("lifecycle notifications=%v", delivered)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTamperedSignedEventRejectedByAPI(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`SELECT account_id::text FROM runs`).WithArgs("run-1", "lease-1").WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow("account-a"))
	event := v1.Event{ID: "run-1:1", Sequence: 1, Type: "state", State: v1.JobRunning, Timestamp: time.Now().UTC(), Message: "started"}
	event.RunID = "run-1"
	event.Signature, _ = events.Sign([]byte("lease-1"), event)
	event.State = v1.JobFailed
	body := mustJSON(t, event)
	server := NewServer(store, provider.NewRegistry())
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/run-1/events", bytes.NewReader(body))
	req.Header.Set("Authorization", "VMBox run-1.lease-1")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStartupReconciliationEnforcesMaxTTLWithExactOwner(t *testing.T) {
	now := time.Now().UTC()
	store, mock := testStore(t)
	created := now.Add(-2 * time.Hour)
	request := []byte(`{"provider":"fake","box":"worker","command":["tool"],"lifecycle":{"maxTtl":"1h"}}`)
	mock.ExpectQuery(`SELECT account_id::text,id::text FROM runs`).WillReturnRows(sqlmock.NewRows([]string{"account_id", "id"}).AddRow("account-a", "run-1"))
	mock.ExpectQuery(`SELECT id::text,account_id::text`).WithArgs("account-a", "run-1").WillReturnRows(sqlmock.NewRows(runColumns()).AddRow(runRow("account-a", "run-1", request, v1.JobRunning, created)...))
	mock.ExpectExec(`UPDATE runs SET state=`).WithArgs("account-a", "run-1", v1.JobCleaningUp, "box-1", "maximum TTL expired", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE runs SET state=`).WithArgs("account-a", "run-1", v1.JobDeleted, "box-1", "maximum TTL expired", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	providerFake := &fakeProvider{}
	server := NewServer(store, provider.NewRegistry())
	server.Deliver = func(context.Context, v1.Run, string, v1.JobState, string, string) {}
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return providerFake, nil }
	if err := server.ReconcileNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if providerFake.deleted != (provider.Owner{AccountID: "account-a", BoxID: "worker", RunID: "run-1", Lease: "lease-1"}) {
		t.Fatalf("delete owner=%+v", providerFake.deleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestImmutableImageAcceptsDockerImageIDOnlyForDocker(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	if !immutableImage("docker", digest) {
		t.Fatal("valid Docker image ID rejected")
	}
	if immutableImage("railway", digest) || immutableImage("docker", "sha256:not-a-digest") {
		t.Fatal("invalid hosted or malformed image accepted")
	}
}

func TestAuthorizationValueRequiresExactScheme(t *testing.T) {
	if value, ok := authorizationValue("Bearer token", "Bearer"); !ok || value != "token" {
		t.Fatalf("valid authorization rejected: %q %v", value, ok)
	}
	for _, header := range []string{"token", "bearer token", "Bearer", "Bearer   "} {
		if _, ok := authorizationValue(header, "Bearer"); ok {
			t.Fatalf("invalid authorization accepted: %q", header)
		}
	}
}

func TestInventoryHidesEveryFleetSlotServiceAndSanitizesExternalBoxes(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprint(cached), func(t *testing.T) {
			store, mock := testStore(t)
			now := time.Now().UTC()
			logicalColumns := []string{"id", "account_id", "owner_user_id", "name", "provider", "provider_credential", "default_agent", "role", "state", "volume_id", "volume_name", "slot_id", "assignment_generation", "lease_owner", "lease_expires_at", "restoration_state", "failure_reason", "created_at", "updated_at", "tools"}
			mock.ExpectQuery(`FROM logical_boxes WHERE account_id=\$1 AND provider=\$2 AND provider_credential=\$3`).WithArgs("account-a", "fake", "primary").WillReturnRows(sqlmock.NewRows(logicalColumns).AddRow("logical-1", "account-a", "user-a", "occupied-workspace", "fake", "primary", "claude", "worker", "running", "volume-1", "workspace-data", "slot-2", int64(1), "", nil, "", "", now, now, "[]"))
			mock.ExpectQuery(`SELECT service_id FROM compute_slots`).WithArgs("account-a", "fake", "primary").WillReturnRows(sqlmock.NewRows([]string{"service_id"}).AddRow("service-free").AddRow("service-occupied"))
			providerFake := &fakeProvider{boxes: []provider.Box{
				{ID: "service-free", Name: "fleet-slot-1", State: provider.StateRunning},
				{ID: "service-occupied", Name: "fleet-slot-2", State: provider.StateRunning},
				{ID: "manual-1", Name: "manual-box", State: provider.StateRunning, Owner: provider.Owner{Lease: "must-not-leak"}, Connection: provider.Connection{Endpoint: "must-not-leak"}},
			}}
			var resolved provider.Provider = providerFake
			if cached {
				resolved = &inventoryObservationFixture{fakeProvider: providerFake, observed: now}
			}
			server := NewServer(store, provider.NewRegistry())
			server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return resolved, nil }
			request := httptest.NewRequest(http.MethodGet, "/v1/inventory?provider=fake&providerCredential=primary", nil)
			response := httptest.NewRecorder()
			server.boxInventoryHandler(response, request, Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"})
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var inventory v1.BoxInventory
			if err := json.Unmarshal(response.Body.Bytes(), &inventory); err != nil {
				t.Fatal(err)
			}
			if cached && (inventory.Infrastructure == nil || !inventory.Infrastructure.Stale || !inventory.Infrastructure.Available) {
				t.Fatal("observation status omitted")
			}
			if len(inventory.LogicalBoxes) != 1 || inventory.LogicalBoxes[0].Name != "occupied-workspace" {
				t.Fatalf("logical boxes=%+v", inventory.LogicalBoxes)
			}
			if len(inventory.ConnectedBoxes) != 1 || inventory.ConnectedBoxes[0].Name != "manual-box" || inventory.ConnectedBoxes[0].Management != "external" {
				t.Fatalf("external boxes=%+v", inventory.ConnectedBoxes)
			}
			if strings.Contains(response.Body.String(), "fleet-slot") || strings.Contains(response.Body.String(), "must-not-leak") {
				t.Fatalf("inventory leaked a fleet slot or provider secret: %s", response.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestControllerUIIsEmbeddedResponsiveAndClosesCleanly(t *testing.T) {
	server := NewServer(nil, provider.NewRegistry())
	for _, test := range []struct {
		path        string
		contentType string
		contains    []string
	}{
		{path: "/", contentType: "text/html", contains: []string{"width=device-width", "Index of /controller", "vmbox BOX", "app.js"}},
		{path: "/app.css", contentType: "text/css", contains: []string{"@media(max-width:600px)", "[hidden]"}},
		{path: "/controller.css", contentType: "text/css", contains: []string{"@media(max-width:600px)", "table-layout:fixed"}},
		{path: "/app.js", contentType: "text/javascript", contains: []string{"/v1/provider-schemas", "If-Match", "defaultAgent", "epoch++", "agentModel"}},
		{path: "/model-picker.js", contentType: "text/javascript", contains: []string{"model-picker-options", "aria-autocomplete"}},
		{path: "/chat", contentType: "text/html", contains: []string{"chat-clear-context", "Clear context"}},
		{path: "/chat.js", contentType: "text/javascript", contains: []string{"#chat-clear-context", "/messages/clear-context", "agentModel"}},
		{path: "/favicon.svg", contentType: "image/svg+xml", contains: []string{"<svg", "#146c5c", "#f4f1ea"}},
		{path: "/favicon.ico", contentType: "image/svg+xml", contains: []string{"<svg", "#146c5c", "#f4f1ea"}},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", test.path, response.Code, response.Body.String())
		}
		if !strings.HasPrefix(response.Header().Get("Content-Type"), test.contentType) {
			t.Fatalf("%s content-type=%q", test.path, response.Header().Get("Content-Type"))
		}
		if !strings.Contains(response.Header().Get("Content-Security-Policy"), "connect-src 'self'") {
			t.Fatalf("%s missing same-origin CSP", test.path)
		}
		for _, expected := range test.contains {
			if !strings.Contains(response.Body.String(), expected) {
				t.Fatalf("%s missing %q", test.path, expected)
			}
		}
		if test.path == "/" && strings.Contains(response.Body.String(), "(active)") {
			t.Fatalf("%s contains unstable active profile suffix", test.path)
		}
		if test.path == "/app.js" && (strings.Contains(response.Body.String(), "style=") || strings.Contains(response.Body.String(), ".style.")) {
			t.Fatalf("%s contains inline styling blocked by the controller CSP", test.path)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/unknown-ui-route", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown route status=%d", response.Code)
	}
}

func TestInstructionPresetUIKeepsCreationCompact(t *testing.T) {
	server := NewServer(nil, provider.NewRegistry())
	for _, path := range []string{"/", "/chat"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		body := response.Body.String()
		if !strings.Contains(body, `id="create-instructions-editor" hidden`) {
			t.Fatalf("%s does not keep the box-specific Markdown editor hidden by default", path)
		}
		for _, decoration := range []string{
			"Preview the selected Markdown and optionally edit",
			"Reusable Markdown guidance, account-scoped",
			"· copied into the box at creation</summary>",
		} {
			if strings.Contains(body, decoration) {
				t.Fatalf("%s still contains decorative preset copy %q", path, decoration)
			}
		}
	}
	for _, path := range []string{"/app.js", "/chat.js"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		for _, decoration := range []string{
			"The account-default preset is copied at creation",
			"No managed instructions are written for this box",
			"No presets yet. Add one below.",
		} {
			if strings.Contains(response.Body.String(), decoration) {
				t.Fatalf("%s still contains decorative preset copy %q", path, decoration)
			}
		}
	}
}

type inventoryObservationFixture struct {
	*fakeProvider
	observed time.Time
}

func (p *inventoryObservationFixture) List(context.Context) ([]provider.Box, error) {
	panic("inventory status invoked provider List")
}
func (p *inventoryObservationFixture) ObserveInventory(context.Context) (provider.InventoryObservation, error) {
	return provider.InventoryObservation{Boxes: p.boxes, Available: true, ObservedAt: p.observed, Stale: true, RefreshFailed: true}, nil
}
