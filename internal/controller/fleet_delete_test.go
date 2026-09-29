package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/secrets"
)

type deletionProvider struct {
	fakeProvider
	attached   *provider.Storage
	inspectErr error
	deleteErr  error
	started    chan struct{}
	release    chan struct{}
	deletes    atomic.Int32
	flushes    atomic.Int32
	sanitized  atomic.Int32
}

type sharedDeletionProvider struct{ *deletionProvider }

func (p *sharedDeletionProvider) Name() string { return "shared-worker" }

func (p *deletionProvider) AttachedStorage(context.Context, string) (*provider.Storage, error) {
	return p.attached, p.inspectErr
}
func (p *deletionProvider) Exec(context.Context, string, []string, provider.ExecOptions) (provider.ExecResult, error) {
	p.flushes.Add(1)
	return provider.ExecResult{}, nil
}
func (p *deletionProvider) DetachStorage(context.Context, string, provider.Storage) error {
	p.attached = nil
	return nil
}
func (p *deletionProvider) SanitizeSlot(context.Context, string) error {
	p.sanitized.Add(1)
	return nil
}
func (p *deletionProvider) DeleteStorage(ctx context.Context, storage provider.Storage, owner provider.Owner) error {
	if storage.ID == "" || owner.AccountID == "" || owner.BoxID == "" {
		return fmt.Errorf("unscoped deletion")
	}
	p.deletes.Add(1)
	if p.started != nil {
		close(p.started)
		select {
		case <-p.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return p.deleteErr
}

func TestDurableDeletionPostgres(t *testing.T) {
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
	ns := "delete_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = store.DB.ExecContext(ctx, "CREATE SCHEMA "+ns); err != nil {
		t.Fatal(err)
	}
	defer store.DB.ExecContext(context.Background(), "DROP SCHEMA "+ns+" CASCADE")
	if _, err = store.DB.ExecContext(ctx, "SET search_path TO "+ns); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store.Envelope, _ = secrets.New(make([]byte, 32))
	owner, err := store.Bootstrap(ctx, "deletion", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	ordinal := 0
	makeBox := func(t *testing.T, name string, assigned bool) (string, string) {
		t.Helper()
		id, slot := uuid(), ""
		ordinal++
		if assigned {
			slot = uuid()
			_, err = store.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,ordinal,state,service_id,assignment_generation,fencing_token) VALUES($1::uuid,$2,'fake',$3,'draining',$1::text,1,'fence')`, slot, owner.AccountID, ordinal)
			if err != nil {
				t.Fatal(err)
			}
		}
		fence := ""
		if assigned {
			fence = "fence"
		}
		_, err = store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,slot_id,assignment_generation,fencing_token) VALUES($1,$2,$3,$4,'fake','deleting',$5,$5,NULLIF($6,'')::uuid,1,NULLIF($7,''))`, id, owner.AccountID, owner.UserID, name, "volume-"+id, slot, fence)
		if err != nil {
			t.Fatal(err)
		}
		return id, slot
	}
	serverFor := func(p *deletionProvider) *Server { return NewServer(store, provider.NewRegistry(p)) }
	t.Run("failed attaching creation can be cancelled and its volume removed", func(t *testing.T) {
		id, slot := makeBox(t, "failed-creation", true)
		if _, err := store.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='attaching',failure_reason='provider access check failed' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		p := &deletionProvider{attached: &provider.Storage{ID: "volume-" + id}}
		s := serverFor(p)
		s.activeCreations[owner.AccountID+":"+id] = struct{}{}
		if _, err := s.queueLogicalBoxDelete(ctx, owner, id, "failed-creation"); err == nil {
			t.Fatal("active creation was cancelled while provider work could still be running")
		}
		delete(s.activeCreations, owner.AccountID+":"+id)
		box, err := s.queueLogicalBoxDelete(ctx, owner, id, "failed-creation")
		if err != nil || box.State != "deleting" {
			t.Fatalf("cancel failed creation: box=%+v err=%v", box, err)
		}
		if err := s.resumeLogicalBoxDelete(ctx, owner, id); err != nil {
			t.Fatal(err)
		}
		if p.deletes.Load() != 1 || p.flushes.Load() != 1 || p.attached != nil {
			t.Fatalf("failed creation did not safely release its attached volume: deletes=%d flushes=%d attached=%v", p.deletes.Load(), p.flushes.Load(), p.attached)
		}
		var count int
		if err := store.DB.QueryRowContext(ctx, `SELECT count(*) FROM logical_boxes WHERE id=$1`, id).Scan(&count); err != nil || count != 0 {
			t.Fatal("failed creation was retained", count, err)
		}
		var state string
		if err := store.DB.QueryRowContext(ctx, `SELECT state FROM compute_slots WHERE id=$1`, slot).Scan(&state); err != nil || state != "free" {
			t.Fatal("slot was not released", state, err)
		}
	})
	t.Run("failed creation before volume allocation can be cancelled", func(t *testing.T) {
		id, slot := makeBox(t, "unmaterialized-creation", true)
		if _, err := store.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='attaching',volume_id='pending:' || id::text,volume_name='pending:unmaterialized-creation',restoration_state='creation-reserved',failure_reason='slot service not found' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		p := &deletionProvider{}
		s := serverFor(p)
		deleteRequest := func() *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodDelete, "/volume", strings.NewReader(`{"confirmation":"unmaterialized-creation"}`)).WithContext(ctx)
			r.SetPathValue("id", id)
			w := httptest.NewRecorder()
			s.deleteLogicalBoxVolumeHandler(w, r, owner)
			return w
		}
		s.activeCreations[owner.AccountID+":"+id] = struct{}{}
		if w := deleteRequest(); w.Code != http.StatusConflict {
			t.Fatalf("active creation delete HTTP %d", w.Code)
		}
		delete(s.activeCreations, owner.AccountID+":"+id)
		if w := deleteRequest(); w.Code != http.StatusAccepted {
			t.Fatalf("failed creation delete HTTP %d: %s", w.Code, w.Body.String())
		}
		var count int
		if err := store.DB.QueryRowContext(ctx, `SELECT count(*) FROM logical_boxes WHERE id=$1`, id).Scan(&count); err != nil || count != 0 {
			t.Fatal("unmaterialized box was retained", count, err)
		}
		var state, health string
		if err := store.DB.QueryRowContext(ctx, `SELECT state,health FROM compute_slots WHERE id=$1`, slot).Scan(&state, &health); err != nil || state != "free" || health != "unhealthy" {
			t.Fatal("slot was not safely released", state, health, err)
		}
		if p.deletes.Load() != 0 {
			t.Fatal("provider volume deletion attempted for an unmaterialized box")
		}
	})
	t.Run("detached recovery and coworker references", func(t *testing.T) {
		id, slot := makeBox(t, "retired", true)
		sibling, _ := makeBox(t, "sibling", false)
		for _, box := range []string{id, sibling} {
			_, err = store.DB.ExecContext(ctx, `INSERT INTO coworkers(account_id,box_id,token_hash,encrypted_token) VALUES($1,$2,$3,'synthetic')`, owner.AccountID, box, []byte(box))
			if err != nil {
				t.Fatal(err)
			}
		}
		_, err = store.DB.ExecContext(ctx, `INSERT INTO coworker_events(account_id,recipient_box_id,sender_box_id,message_key,kind,data) VALUES($1,$2,$3,'sent','message','{"text":"preserve"}'),($1,$3,$2,'inbox','message','{}')`, owner.AccountID, sibling, id)
		if err != nil {
			t.Fatal(err)
		}
		p := &deletionProvider{}
		s := serverFor(p)
		if err = s.resumeLogicalBoxDelete(ctx, owner, id); err != nil {
			t.Fatal(err)
		}
		if p.deletes.Load() != 1 || p.flushes.Load() != 0 || p.sanitized.Load() != 1 {
			t.Fatal("detached recovery replayed flush or skipped cleanup")
		}
		var state string
		if err = store.DB.QueryRowContext(ctx, "SELECT state FROM compute_slots WHERE id=$1", slot).Scan(&state); err != nil || state != "free" {
			t.Fatal(state, err)
		}
		var count int
		store.DB.QueryRowContext(ctx, "SELECT count(*) FROM logical_boxes WHERE id=$1", id).Scan(&count)
		if count != 0 {
			t.Fatal("box retained")
		}
		store.DB.QueryRowContext(ctx, `SELECT count(*) FROM coworker_events WHERE recipient_box_id=$1 AND sender_box_id IS NULL AND data->>'text'='preserve'`, sibling).Scan(&count)
		if count != 1 {
			t.Fatal("sibling message lost")
		}
	})
	t.Run("deleted box releases only its unshared chat attachments", func(t *testing.T) {
		deletedBox, _ := makeBox(t, "media-delete", false)
		sibling, _ := makeBox(t, "media-sibling", false)
		privateImage, sharedImage := uuid(), uuid()
		for _, imageID := range []string{privateImage, sharedImage} {
			if _, err := store.DB.ExecContext(ctx, `INSERT INTO run_once_images(id,account_id,media_type,data,download_token,expires_at) VALUES($1,$2,'image/png',$3,'test-token',now()+interval '1 day')`, imageID, owner.AccountID, []byte("image")); err != nil {
				t.Fatal(err)
			}
		}
		for _, box := range []string{deletedBox, sibling} {
			task, message := uuid(), uuid()
			if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_tasks(id,account_id,logical_box_id,user_id,requested_role,agent,session_name,prompt,state,idempotency_key) VALUES($1,$2,$3,$4,'owner','codex','test','test','active',$5)`, task, owner.AccountID, box, owner.UserID, "task-"+task); err != nil {
				t.Fatal(err)
			}
			if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_messages(id,account_id,task_id,user_id,direction,body,state,idempotency_key) VALUES($1,$2,$3,$4,'user','test','delivered',$5)`, message, owner.AccountID, task, owner.UserID, "message-"+message); err != nil {
				t.Fatal(err)
			}
			if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_message_images(message_id,account_id,image_id,ordinal) VALUES($1,$2,$3,1)`, message, owner.AccountID, sharedImage); err != nil {
				t.Fatal(err)
			}
			if box == deletedBox {
				if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_message_images(message_id,account_id,image_id,ordinal) VALUES($1,$2,$3,2)`, message, owner.AccountID, privateImage); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := serverFor(&deletionProvider{}).resumeLogicalBoxDelete(ctx, owner, deletedBox); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := store.DB.QueryRowContext(ctx, `SELECT count(*) FROM run_once_images WHERE id=$1`, privateImage).Scan(&count); err != nil || count != 0 {
			t.Fatalf("private attachment survived deletion: count=%d err=%v", count, err)
		}
		if err := store.DB.QueryRowContext(ctx, `SELECT count(*) FROM run_once_images WHERE id=$1`, sharedImage).Scan(&count); err != nil || count != 1 {
			t.Fatalf("shared attachment was lost: count=%d err=%v", count, err)
		}
		if err := serverFor(&deletionProvider{}).resumeLogicalBoxDelete(ctx, owner, sibling); err != nil {
			t.Fatal(err)
		}
		if err := store.DB.QueryRowContext(ctx, `SELECT count(*) FROM run_once_images WHERE id=$1`, sharedImage).Scan(&count); err != nil || count != 0 {
			t.Fatalf("last attachment reference was not released: count=%d err=%v", count, err)
		}
	})
	t.Run("expired unattached uploads are pruned but chat media remains", func(t *testing.T) {
		box, _ := makeBox(t, "media-expiry", false)
		task, message := uuid(), uuid()
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_tasks(id,account_id,logical_box_id,user_id,requested_role,agent,session_name,prompt,state,idempotency_key) VALUES($1,$2,$3,$4,'owner','codex','test','test','active',$5)`, task, owner.AccountID, box, owner.UserID, "task-"+task); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_messages(id,account_id,task_id,user_id,direction,body,state,idempotency_key) VALUES($1,$2,$3,$4,'user','test','delivered',$5)`, message, owner.AccountID, task, owner.UserID, "message-"+message); err != nil {
			t.Fatal(err)
		}
		expiredUnused, freshUnused, expiredAttached := uuid(), uuid(), uuid()
		for _, entry := range []struct{ id, expiry string }{{expiredUnused, "-1 day"}, {freshUnused, "+1 day"}, {expiredAttached, "-1 day"}} {
			if _, err := store.DB.ExecContext(ctx, `INSERT INTO run_once_images(id,account_id,media_type,data,download_token,expires_at) VALUES($1,$2,'image/png',$3,'test-token',now()+$4::interval)`, entry.id, owner.AccountID, []byte("image"), entry.expiry); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_message_images(message_id,account_id,image_id,ordinal) VALUES($1,$2,$3,1)`, message, owner.AccountID, expiredAttached); err != nil {
			t.Fatal(err)
		}
		if err := store.pruneExpiredUnusedAttachments(ctx, owner.AccountID); err != nil {
			t.Fatal(err)
		}
		for _, entry := range []struct {
			id   string
			want int
		}{{expiredUnused, 0}, {freshUnused, 1}, {expiredAttached, 1}} {
			var count int
			if err := store.DB.QueryRowContext(ctx, `SELECT count(*) FROM run_once_images WHERE id=$1`, entry.id).Scan(&count); err != nil || count != entry.want {
				t.Fatalf("attachment %s count=%d want=%d err=%v", entry.id, count, entry.want, err)
			}
		}
	})
	t.Run("failure survives cancellation and no second worker", func(t *testing.T) {
		id, _ := makeBox(t, "cancelled", false)
		p := &deletionProvider{started: make(chan struct{}), release: make(chan struct{})}
		s := serverFor(p)
		op, cancelOp := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- s.resumeLogicalBoxDelete(op, owner, id) }()
		select {
		case <-p.started:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if err = s.resumeLogicalBoxDelete(ctx, owner, id); err != nil {
			t.Fatal(err)
		}
		if p.deletes.Load() != 1 {
			t.Fatal("duplicate provider deletion")
		}
		cancelOp()
		if err = <-done; err == nil {
			t.Fatal("cancellation ignored")
		}
		var failure string
		var expires time.Time
		if err = store.DB.QueryRowContext(ctx, "SELECT failure_reason,lease_expires_at FROM logical_boxes WHERE id=$1", id).Scan(&failure, &expires); err != nil || !strings.Contains(failure, "canceled") || !expires.After(time.Now()) {
			t.Fatal("failure/backoff not persisted", failure, err)
		}
		if _, err = store.DB.ExecContext(ctx, "UPDATE logical_boxes SET lease_expires_at=now()-interval '1 second' WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
		p.started = nil
		if err = serverFor(p).resumeLogicalBoxDelete(ctx, owner, id); err != nil {
			t.Fatal("restart retry failed", err)
		}
	})
	t.Run("wrong volume and SSH inspection failure are safe", func(t *testing.T) {
		for _, p := range []*deletionProvider{{attached: &provider.Storage{ID: "other-volume"}}, {inspectErr: errors.New("permission denied")}} {
			id, _ := makeBox(t, uuid(), true)
			if err = serverFor(p).resumeLogicalBoxDelete(ctx, owner, id); err == nil {
				t.Fatal("unsafe deletion accepted")
			}
			if p.deletes.Load() != 0 || p.flushes.Load() != 0 || p.sanitized.Load() != 0 {
				t.Fatal("unsafe provider mutation")
			}
		}
	})
	t.Run("attached volume is flushed and detached", func(t *testing.T) {
		id, _ := makeBox(t, "attached", true)
		p := &deletionProvider{attached: &provider.Storage{ID: "volume-" + id}}
		if err = serverFor(p).resumeLogicalBoxDelete(ctx, owner, id); err != nil {
			t.Fatal(err)
		}
		if p.flushes.Load() != 1 || p.deletes.Load() != 1 || p.sanitized.Load() != 1 || p.attached != nil {
			t.Fatal("incomplete attached-volume deletion")
		}
	})
	t.Run("shared worker deletion does not require a workspace runtime upload", func(t *testing.T) {
		id, _ := makeBox(t, "shared-delete", true)
		if _, err := store.DB.ExecContext(ctx, `UPDATE logical_boxes SET provider='shared-worker' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		p := &deletionProvider{attached: &provider.Storage{ID: "volume-" + id}}
		s := NewServer(store, provider.NewRegistry(&sharedDeletionProvider{p}))
		if err := s.resumeLogicalBoxDelete(ctx, owner, id); err != nil {
			t.Fatal(err)
		}
		if p.flushes.Load() != 0 || p.deletes.Load() != 1 || p.sanitized.Load() != 1 || p.attached != nil {
			t.Fatalf("shared deletion must stop through detach without a workspace runtime: flushes=%d deletes=%d sanitized=%d attached=%v", p.flushes.Load(), p.deletes.Load(), p.sanitized.Load(), p.attached)
		}
	})
	t.Run("stale slot generation blocks provider mutations", func(t *testing.T) {
		id, slot := makeBox(t, "stale-slot", true)
		a, err := store.BeginLogicalBoxRelease(ctx, owner, id, "deleting")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.DB.ExecContext(ctx, "UPDATE logical_boxes SET lease_owner='claim' WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
		if _, err = store.DB.ExecContext(ctx, "UPDATE compute_slots SET assignment_generation=2 WHERE id=$1", slot); err != nil {
			t.Fatal(err)
		}
		p := &deletionProvider{}
		if err = serverFor(p).completeLogicalBoxDelete(ctx, owner, a, "claim"); err == nil {
			t.Fatal("stale generation accepted")
		}
		if p.deletes.Load() != 0 || p.flushes.Load() != 0 || p.sanitized.Load() != 0 {
			t.Fatal("stale worker performed mutation")
		}
	})
	t.Run("HTTP handoff survives client cancellation", func(t *testing.T) {
		id, _ := makeBox(t, "handoff", false)
		s := serverFor(&deletionProvider{})
		started := make(chan context.Context, 1)
		release := make(chan struct{})
		finished := make(chan struct{})
		s.StartDelete = func(op context.Context, _ Principal, _ string) error {
			started <- op
			<-release
			close(finished)
			return nil
		}
		rctx, rcancel := context.WithCancel(ctx)
		r := httptest.NewRequest(http.MethodDelete, "/volume", strings.NewReader(`{"confirmation":"handoff"}`)).WithContext(rctx)
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.deleteLogicalBoxVolumeHandler(w, r, owner)
		if w.Code != http.StatusAccepted {
			t.Fatal(w.Code, w.Body.String())
		}
		op := <-started
		rcancel()
		if op.Err() != nil {
			t.Fatal("HTTP cancellation reached background operation")
		}
		close(release)
		<-finished
	})
	t.Run("restart discovers pending deletes", func(t *testing.T) {
		id, _ := makeBox(t, "restart", false)
		s := serverFor(&deletionProvider{})
		started := make(chan string, 20)
		s.StartDelete = func(_ context.Context, _ Principal, id string) error { started <- id; return nil }
		if err = s.ReconcileLogicalBoxDeletesNow(ctx); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case got := <-started:
				if got == id {
					return
				}
			case <-ctx.Done():
				t.Fatal("pending deletion not recovered")
			}
		}
	})
}
