package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/secrets"
)

func TestRunOncePostgresQueueRecoveryAndCancellation(t *testing.T) {
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
	schema := "run_once_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = admin.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	store, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store.Envelope, _ = secrets.New(make([]byte, 32))
	p, err := store.Bootstrap(ctx, "queue-test", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(store, nil)
	req := runOnceRequest{Agent: "shell", Prompt: "printf actual-test", Provider: "railway", ProviderCredential: "primary"}
	create := func(key string, request runOnceRequest, status int) runOnceRecord {
		t.Helper()
		b, _ := json.Marshal(request)
		r := httptest.NewRequest("POST", "/v1/run-once", bytes.NewReader(b))
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		server.createRunOnce(w, r, p)
		if w.Code != status {
			t.Fatalf("create status %d: %s", w.Code, w.Body.String())
		}
		var result runOnceRecord
		if status == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	first := create("first", req, 200)
	if first.ID == "" || first.State != "queued" {
		t.Fatal("not durably queued")
	}
	replay := create("first", req, 200)
	if replay.ID != first.ID {
		t.Fatal("duplicate run")
	}
	changed := req
	changed.Prompt = "different"
	create("first", changed, 409)
	if err = server.reconcileRunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = store.DB.QueryRowContext(ctx, "SELECT count(*) FROM logical_boxes").Scan(&count); err != nil || count != 0 {
		t.Fatal("no-capacity run created a box")
	}
	r := httptest.NewRequest("POST", "/cancel", nil)
	r.SetPathValue("id", first.ID)
	w := httptest.NewRecorder()
	server.cancelRunOnce(w, r, p)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err = server.reconcileRunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// Simulate a committed box reservation whose response was lost. Recovery
	// must adopt exactly that named box, then enqueue exactly one process task.
	second := create("second", req, 200)
	name := "once-" + strings.ReplaceAll(second.ID, "-", "")
	box, err := store.UpsertLogicalBox(ctx, p, v1.LogicalBox{Name: name, Provider: "railway", ProviderCredential: "primary", State: v1.LogicalBoxHibernated, VolumeID: "test-volume", VolumeName: "test-volume"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = server.reconcileRunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var result []byte
	if err = store.DB.QueryRowContext(ctx, "SELECT result FROM process_tasks WHERE id=$1", second.ID).Scan(&result); err != nil {
		t.Fatal(err)
	}
	var task v1.ProcessTask
	if err = json.Unmarshal(result, &task); err != nil {
		t.Fatal(err)
	}
	if task.LogicalBoxID != box.ID || task.Prompt != req.Prompt || task.State != "queued" || task.Session != "task-"+second.ID {
		t.Fatalf("wrong process: %+v", task)
	}
	if err = store.DB.QueryRowContext(ctx, "SELECT count(*) FROM process_tasks").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate process")
	}
	r.SetPathValue("id", second.ID)
	w = httptest.NewRecorder()
	server.cancelRunOnce(w, r, p)
	if w.Code != 409 {
		t.Fatal("cancelled an already claimed run")
	}
	other := p
	other.AccountID = uuid()
	r.SetPathValue("id", second.ID)
	w = httptest.NewRecorder()
	server.listRunOnce(w, r, other)
	if w.Code != 404 {
		t.Fatal("cross-account run disclosure")
	}
}
