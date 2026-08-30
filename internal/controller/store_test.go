package controller

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	"github.com/DATA-DOG/go-sqlmock"
)

func testStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Store{DB: db}, mock
}

type excludesPlaintext string

func (m excludesPlaintext) Match(value driver.Value) bool {
	text, ok := value.(string)
	return ok && !strings.Contains(text, string(m))
}

func runColumns() []string {
	return []string{"id", "account_id", "box_id", "provider", "state", "request", "external_reference", "created_at", "updated_at", "started_at", "finished_at", "exit_code", "summary", "last_output_at", "last_heartbeat_at", "last_activity", "lease"}
}

func runRow(account, id string, request []byte, state v1.JobState, created time.Time) []driver.Value {
	return []driver.Value{id, account, "box-1", "docker", string(state), request, "", created, created, nil, nil, nil, nil, nil, nil, nil, "lease-1"}
}

func TestGetRunIsTenantScopedAndRestoresLifecycleDurations(t *testing.T) {
	store, mock := testStore(t)
	created := time.Now().UTC()
	request := []byte(`{"provider":"docker","command":["tool","two words"],"lifecycle":{"maxTtl":"45m","gracePeriod":"3m"}}`)
	mock.ExpectQuery(`SELECT id::text,account_id::text`).WithArgs("account-a", "run-1").WillReturnRows(sqlmock.NewRows(runColumns()).AddRow(runRow("account-a", "run-1", request, v1.JobRunning, created)...))
	run, err := store.GetRun(context.Background(), "account-a", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.AccountID != "account-a" || run.Request.Lifecycle.MaxTTL != 45*time.Minute || run.Request.Lifecycle.GracePeriod != 3*time.Minute {
		t.Fatalf("unexpected run: %+v", run)
	}
	if len(run.Request.Command) != 2 || run.Request.Command[1] != "two words" {
		t.Fatalf("argv changed: %#v", run.Request.Command)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateRunIdempotencyIsAccountScoped(t *testing.T) {
	store, mock := testStore(t)
	req := v1.CreateRunRequest{Provider: "docker", Command: []string{"printf", "%s", "$HOME;"}}
	normalized := req
	if err := normalized.Lifecycle.Normalize(); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec(`INSERT INTO runs`).WithArgs(sqlmock.AnyArg(), "account-a", "user-a", "docker", v1.JobQueued, sqlmock.AnyArg(), nil, "same-key", sqlmock.AnyArg()).WillReturnError(errors.New("unique violation"))
	created := time.Now().UTC()
	request, _ := json.Marshal(normalized)
	mock.ExpectQuery(`FROM runs WHERE account_id=\$1 AND idempotency_key=\$2`).WithArgs("account-a", "same-key").WillReturnRows(sqlmock.NewRows([]string{"id", "provider", "state", "request", "external_reference", "lease", "created_at", "updated_at"}).AddRow("run-existing", "docker", string(v1.JobQueued), request, "", "lease", created, created))
	run, reused, err := store.CreateRun(context.Background(), Principal{AccountID: "account-a", UserID: "user-a"}, req, "same-key")
	if err != nil || !reused || run.ID != "run-existing" || run.AccountID != "account-a" {
		t.Fatalf("run=%+v reused=%v err=%v", run, reused, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAnswerIsExactlyOnceAuditedAndResumesRun(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "account-a", UserID: "user-a"}
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE questions SET state='answered'`).WithArgs("account-a", "user-a", "q_1", "preserve v1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO audit_log`).WithArgs("account-a", "user-a", "q_1").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`UPDATE runs SET state='running'`).WithArgs("account-a", "q_1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := store.Answer(context.Background(), p, "q_1", "preserve v1"); err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE questions SET state='answered'`).WithArgs("account-a", "user-a", "q_1", "duplicate").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	if err := store.Answer(context.Background(), p, "q_1", "duplicate"); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("duplicate answer error=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRolesAreExactlyOwnerAndUser(t *testing.T) {
	store := &Store{}
	for _, role := range []string{"admin", "member", ""} {
		if _, err := store.CreateUser(context.Background(), Principal{}, v1.CreateUserRequest{Subject: "person", Role: role}); err == nil {
			t.Fatalf("role %q accepted", role)
		}
	}
}

func TestProviderCredentialDecryptsOnlyInOwningAccount(t *testing.T) {
	store, mock := testStore(t)
	envelope, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store.Envelope = envelope
	sealed, err := envelope.Seal("account-a", []byte(`{"token":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	columns := []string{"id", "account_id", "provider", "name", "encrypted_value", "config", "created_at", "updated_at"}
	mock.ExpectQuery(`FROM provider_credentials WHERE account_id=\$1`).WithArgs("account-a", "railway", "primary").WillReturnRows(sqlmock.NewRows(columns).AddRow("cred-1", "account-a", "railway", "primary", sealed, []byte(`{"projectId":"p"}`), now, now))
	credential, err := store.ProviderCredential(context.Background(), "account-a", "railway", "primary")
	if err != nil || !strings.Contains(string(credential.Secret), "secret") {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
	mock.ExpectQuery(`FROM provider_credentials WHERE account_id=\$1`).WithArgs("account-b", "railway", "primary").WillReturnRows(sqlmock.NewRows(columns))
	if _, err := store.ProviderCredential(context.Background(), "account-b", "railway", "primary"); err == nil {
		t.Fatal("cross-tenant credential lookup succeeded")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProviderCredentialCRUDNeverReturnsOrStoresPlaintext(t *testing.T) {
	store, mock := testStore(t)
	envelope, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store.Envelope = envelope
	now := time.Now().UTC()
	secret := json.RawMessage(`{"token":"never-store-this-plaintext"}`)
	config := json.RawMessage(`{"projectId":"p"}`)
	mock.ExpectQuery(`INSERT INTO provider_credentials`).WithArgs(sqlmock.AnyArg(), "account-a", "railway", "primary", excludesPlaintext("never-store-this-plaintext"), config).WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow("00000000-0000-4000-8000-000000000001", now, now))
	mock.ExpectExec(`INSERT INTO audit_log`).WithArgs("account-a", "user-a", "00000000-0000-4000-8000-000000000001", "railway", "primary").WillReturnResult(sqlmock.NewResult(1, 1))
	value, err := store.PutProviderCredential(context.Background(), Principal{AccountID: "account-a", UserID: "user-a"}, "railway", "primary", v1.PutProviderCredentialRequest{Secret: secret, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(value)
	if bytes.Contains(encoded, []byte("never-store-this-plaintext")) {
		t.Fatalf("API metadata exposed secret: %s", encoded)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
