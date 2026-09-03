package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/events"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	"github.com/DATA-DOG/go-sqlmock"
)

type fakeProvider struct {
	created provider.CreateRequest
	argv    []string
	deleted provider.Owner
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
func (*fakeProvider) List(context.Context) ([]provider.Box, error) { return nil, nil }
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

func TestTelegramIntegrationAuthenticatesMapsAndAuditsAnswer(t *testing.T) {
	store, mock := testStore(t)
	envelope, err := secrets.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	store.Envelope = envelope
	secret, err := envelope.Seal("account-a", []byte(`{"token":"bot-token","webhookSecret":"hook-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	columns := []string{"id", "account_id", "kind", "name", "encrypted_secret", "config", "allowed_users", "allowed_chats", "enabled", "created_at", "updated_at"}
	config := []byte(`{"chatId":"9","userMap":{"7":"user-7"}}`)
	mock.ExpectQuery(`SELECT id::text,account_id::text,kind,name,encrypted_secret`).WithArgs("account-a", "telegram", "team").WillReturnRows(sqlmock.NewRows(columns).AddRow("destination-1", "account-a", "telegram", "team", secret, config, []byte(`["7"]`), []byte(`["9"]`), true, time.Now(), time.Now()))
	mock.ExpectQuery(`SELECT role,subject FROM users`).WithArgs("account-a", "user-7").WillReturnRows(sqlmock.NewRows([]string{"role", "subject"}).AddRow("user", "alice"))
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE questions SET state='answered'`).WithArgs("account-a", "user-7", "q_1", "ship it").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO audit_log`).WithArgs("account-a", "user-7", "q_1").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`UPDATE runs SET state='running'`).WithArgs("account-a", "q_1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	server := NewServer(store, provider.NewRegistry())
	req := httptest.NewRequest(http.MethodPost, "/v1/integrations/telegram/account-a/team", strings.NewReader(`{"message":{"text":"/answer q_1 ship it","from":{"id":7},"chat":{"id":9}}}`))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "hook-secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestControllerUIIsEmbeddedResponsiveAndClosesCleanly(t *testing.T) {
	server := NewServer(nil, provider.NewRegistry())
	for _, test := range []struct {
		path        string
		contentType string
		contains    []string
	}{
		{path: "/", contentType: "text/html", contains: []string{"viewport-fit=cover", "Message the agent", "app.js"}},
		{path: "/app.css", contentType: "text/css", contains: []string{"@media(max-width:720px)", "env(safe-area-inset-bottom)", ".terminal-guide"}},
		{path: "/app.js", contentType: "text/javascript", contains: []string{"sessionStorage", "pagehide", "pageshow", "controller.abort()", "/terminal/input"}},
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
	}
	request := httptest.NewRequest(http.MethodGet, "/unknown-ui-route", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown route status=%d", response.Code)
	}
}
