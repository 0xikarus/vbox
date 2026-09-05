package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/secrets"
)

// Run only against a disposable database. Each invocation uses its own schema.
func TestNativeControllerPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set VMBOX_TEST_DATABASE_URL to a disposable PostgreSQL instance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.DB.SetMaxOpenConns(1)
	schemaName := "native_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = s.DB.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatal(err)
	}
	defer s.DB.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
	if _, err = s.DB.ExecContext(ctx, "SET search_path TO "+schemaName); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("additive migration not repeatable", err)
	}
	s.Envelope, _ = secrets.New(make([]byte, 32))
	p, err := s.Bootstrap(ctx, "native", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	box := uuid()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,provider_credential,state,volume_id,volume_name,assignment_generation,fencing_token) VALUES($1,$2,$3,'native','railway','primary','running','test-volume','test-volume',1,'test-fence')`, box, p.AccountID, p.UserID)
	if err != nil {
		t.Fatal(err)
	}
	fence := nativeFence(fleetAssignment{Box: v1.LogicalBox{ID: box, AssignmentGeneration: 1}, FencingToken: "test-fence"})
	now := time.Now().UTC().Truncate(time.Microsecond)
	inv := v1.SessionInventory{LogicalBoxID: box, Assignment: fence, State: "live", ObservedAt: now, Sessions: []v1.Session{{ID: "$1", Name: "manual ü", Incarnation: fence + ":$1", Fingerprint: "first"}, {ID: "$2", Name: "sibling", Incarnation: fence + ":$2", Fingerprint: "sibling"}}}
	if err = s.RecordSessionObservation(ctx, p, inv); err != nil {
		t.Fatal(err)
	}
	updates, err := s.SessionUpdates(ctx, p, box)
	if err != nil || len(updates) != 2 {
		t.Fatalf("baseline count=%d err=%v", len(updates), err)
	}
	rev := updates[0].Revision
	inv.ObservedAt = now.Add(time.Second)
	if err = s.RecordSessionObservation(ctx, p, inv); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := s.SessionUpdates(ctx, p, box)
	if unchanged[0].Revision != rev {
		t.Fatal("unchanged observation advanced revision")
	}
	server := NewServer(s, provider.NewRegistry())
	user, err := s.CreateUser(ctx, p, v1.CreateUserRequest{Subject: "reader", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ method, path string }{{"GET", "/v1/logical-boxes/" + box + "/native-connection"}, {"POST", "/v1/logical-boxes/" + box + "/sessions/enable"}, {"PATCH", "/v1/provider-credentials/railway/primary"}} {
		r := httptest.NewRequest(target.method, target.path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+user.Token)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("user role %s returned %d", target.path, w.Code)
		}
	}
	ack := func(principal Principal, revision string) int {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"session":"manual ü","revision":"`+revision+`"}`))
		r.SetPathValue("id", box)
		w := httptest.NewRecorder()
		server.ackUpdateHandler(w, r, principal)
		return w.Code
	}
	if code := ack(p, rev); code != 200 {
		t.Fatalf("ack status=%d", code)
	}
	unread, _ := s.SessionUpdates(ctx, p, box)
	if len(unread) != 1 {
		t.Fatal("ack affected sibling or did not persist")
	}
	other := p
	other.UserID = uuid()
	two, _ := s.SessionUpdates(ctx, other, box)
	if len(two) != 2 {
		t.Fatal("checkpoint leaked between users")
	}
	inv.Sessions[0].Fingerprint = "changed"
	inv.ObservedAt = now.Add(2 * time.Second)
	if err = s.RecordSessionObservation(ctx, p, inv); err != nil {
		t.Fatal(err)
	}
	if code := ack(p, rev); code != 409 {
		t.Fatalf("stale ack status=%d", code)
	}
	old := inv
	old.ObservedAt = now
	old.Sessions = nil
	if err = s.RecordSessionObservation(ctx, p, old); err != nil {
		t.Fatal(err)
	}
	current, _ := s.SessionUpdates(ctx, p, box)
	for _, u := range current {
		if u.State == "exited" {
			t.Fatal("late empty probe overwrote live state")
		}
	}
	inv.ObservedAt = now.Add(3 * time.Second)
	inv.Sessions = inv.Sessions[1:]
	if err = s.RecordSessionObservation(ctx, p, inv); err != nil {
		t.Fatal(err)
	}
	current, _ = s.SessionUpdates(ctx, p, box)
	for _, u := range current {
		if u.Session == "manual ü" && u.State != "exited" {
			t.Fatal("exit not observed")
		}
		if u.Session == "sibling" && u.State == "exited" {
			t.Fatal("sibling ended")
		}
	}
	config := json.RawMessage(`{"projectId":"p","environmentId":"e","image":"old"}`)
	v, err := s.PutProviderCredential(ctx, p, "railway", "primary", v1.PutProviderCredentialRequest{Config: config, Secret: json.RawMessage(`{"token":"integration-test-secret"}`)})
	if err != nil {
		t.Fatal(err)
	}
	edit := patchProviderRequest{Config: map[string]json.RawMessage{"image": json.RawMessage(`"new"`)}}
	updated, err := s.PatchProvider(ctx, p, "railway", "primary", v.UpdatedAt.Format(time.RFC3339Nano), edit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PatchProvider(ctx, p, "railway", "primary", v.UpdatedAt.Format(time.RFC3339Nano), edit); err == nil {
		t.Fatal("concurrent edit accepted")
	}
	credential, err := s.ProviderCredential(ctx, p.AccountID, "railway", "primary")
	if err != nil || !strings.Contains(string(credential.Secret), "integration-test-secret") {
		t.Fatal("secret not preserved")
	}
	edit.Config = map[string]json.RawMessage{"projectId": json.RawMessage(`"other"`)}
	if _, err = s.PatchProvider(ctx, p, "railway", "primary", updated.UpdatedAt.Format(time.RFC3339Nano), edit); err == nil {
		t.Fatal("provider retarget accepted")
	}
	if _, err = s.PutProviderCredential(ctx, p, "railway", "primary", v1.PutProviderCredentialRequest{Config: config, Secret: json.RawMessage(`{"token":"replacement"}`)}); err == nil {
		t.Fatal("legacy PUT bypassed concurrency")
	}
	if err = validateProviderConfig("railway", json.RawMessage(`{"token":"never public"}`)); err == nil {
		t.Fatal("secret field accepted in config")
	}
}
