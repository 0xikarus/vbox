package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func goalTaskTestStore(t *testing.T) (*Store, Principal, string) {
	t.Helper()
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx := context.Background()
	base, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	ns := "goal_task_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = base.DB.ExecContext(ctx, "CREATE SCHEMA "+ns); err != nil {
		base.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { base.DB.ExecContext(context.Background(), "DROP SCHEMA "+ns+" CASCADE"); base.Close() })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", ns)
	u.RawQuery = q.Encode()
	store, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	token := uuid()
	p, err := store.Bootstrap(ctx, "task-test", "owner", token)
	if err != nil {
		t.Fatal(err)
	}
	return store, p, token
}
func testGoalRequest(name string) v1.CreateGoalTaskRequest {
	r := v1.CreateGoalTaskRequest{Name: name, Goal: "Return a fixture result", Agent: "codex"}
	if err := normalizeGoalTask(&r); err != nil {
		panic(err)
	}
	return r
}
func taskHTTP(s *Server, method, path, token string, body any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Idempotency-Key", "fixture-key")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestGoalTaskCurrentAndHistoryPostgres(t *testing.T) {
	store, p, token := goalTaskTestStore(t)
	ctx := context.Background()
	a := goalTaskActor{Principal: p, Kind: "owner", ID: p.UserID}
	states := []string{"queued", "starting", "running", "completed", "failed", "timed_out", "abandoned", "cancelled"}
	for _, state := range states {
		id, _, err := store.createGoalTask(ctx, a, testGoalRequest(state), state)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.DB.ExecContext(ctx, `UPDATE goal_tasks SET state=$2 WHERE id=$1`, id, state); err != nil {
			t.Fatal(err)
		}
	}
	srv := &Server{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for filter, count := range map[string]int{"": 8, "current": 3, "history": 5, "failed": 1} {
		w := taskHTTP(srv, "GET", "/v1/tasks?state="+filter, token, nil)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", filter, w.Code, w.Body.String())
		}
		var result struct {
			Tasks []v1.GoalTask `json:"tasks"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Tasks) != count {
			t.Fatalf("%s: got %d tasks, want %d", filter, len(result.Tasks), count)
		}
		for _, task := range result.Tasks {
			if (filter == "current" && v1.GoalTaskFinished(task.State)) || (filter == "history" && !v1.GoalTaskFinished(task.State)) {
				t.Fatalf("%s includes %s", filter, task.State)
			}
		}
	}
}
func TestGoalTaskQueueAndRetentionPostgres(t *testing.T) {
	store, p, _ := goalTaskTestStore(t)
	ctx := context.Background()
	a := goalTaskActor{Principal: p, Kind: "owner", ID: p.UserID, Name: "owner", AllowControl: true}
	ids := []string{}
	for i := range 12 {
		id, _, err := store.createGoalTask(ctx, a, testGoalRequest(fmt.Sprintf("fixture-%d", i)), fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	var count atomic.Int32
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := store.claimGoalTask(ctx, p.AccountID, id)
			if err != nil {
				t.Error(err)
			}
			if ok {
				count.Add(1)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 4 {
		t.Fatalf("claimed %d tasks, want 4", count.Load())
	}
	var boxes int
	if err := store.DB.QueryRowContext(ctx, `SELECT count(*) FROM logical_boxes`).Scan(&boxes); err != nil || boxes != 0 {
		t.Fatalf("queued/claimed task provisioned box: %d %v", boxes, err)
	}
	var heldID, queuedID string
	store.DB.QueryRowContext(ctx, `SELECT id::text FROM goal_tasks WHERE slot_held LIMIT 1`).Scan(&heldID)
	store.DB.QueryRowContext(ctx, `SELECT id::text FROM goal_tasks WHERE state='queued' ORDER BY created_at LIMIT 1`).Scan(&queuedID)
	queued, err := store.goalTask(ctx, a, queuedID)
	if err != nil || queued.Box != nil || queued.StartedAt != nil || queued.QueuePosition < 1 {
		t.Fatalf("queued=%+v err=%v", queued, err)
	}
	changed, err := store.finishGoalTask(ctx, p.AccountID, heldID, "completed", "fixture result", "", []goalTaskUpload{{Name: "report.txt", MediaType: "text/plain", Data: []byte("fixture")}})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	// Finishing cannot free the place until cleanup confirms slot release.
	if ok, err := store.claimGoalTask(ctx, p.AccountID, queuedID); err != nil || ok {
		t.Fatalf("released too early: %v %v", ok, err)
	}
	if changed, err = store.finishGoalTask(ctx, p.AccountID, heldID, "failed", "late result", "agent", nil); err != nil || changed {
		t.Fatal("outcome overwritten", changed, err)
	}
	srv := &Server{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err = srv.reconcileGoalTask(ctx, heldID); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.claimGoalTask(ctx, p.AccountID, queuedID); err != nil || !ok {
		t.Fatalf("did not release after cleanup: %v %v", ok, err)
	}
	// Replays return the same record; changed parameters conflict.
	replay, used, err := store.createGoalTask(ctx, a, testGoalRequest("fixture-0"), "0")
	if err != nil || !used || replay != ids[0] {
		t.Fatal(replay, used, err)
	}
	_, _, err = store.createGoalTask(ctx, a, testGoalRequest("different"), "0")
	if !errors.Is(err, errGoalTaskIdempotency) {
		t.Fatalf("mismatch: %v", err)
	}
	if _, err = store.DB.ExecContext(ctx, `UPDATE goal_tasks SET delete_at=now()-interval '1 second' WHERE id=$1`, heldID); err != nil {
		t.Fatal(err)
	}
	if err = srv.reconcileGoalTask(ctx, heldID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.goalTask(ctx, a, heldID); !errors.Is(err, errGoalTaskMissing) {
		t.Fatalf("expired task retained: %v", err)
	}
	var files int
	store.DB.QueryRowContext(ctx, `SELECT count(*) FROM goal_task_files WHERE task_id=$1`, heldID).Scan(&files)
	if files != 0 {
		t.Fatal("files retained after task deletion")
	}
}
func TestGoalTaskServiceAndLinkIsolationPostgres(t *testing.T) {
	store, p, ownerToken := goalTaskTestStore(t)
	ctx := context.Background()
	srv := &Server{Store: store, PublicURL: "http://example.test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	profileID := "lp_" + strings.ReplaceAll(uuid(), "-", "")
	_, err := store.DB.ExecContext(ctx, `INSERT INTO login_profiles(account_id,application,name,encrypted_value,public_id) VALUES($1,'codex','private@example.test','fixture',$2)`, p.AccountID, profileID)
	if err != nil {
		t.Fatal(err)
	}
	response := taskHTTP(srv, "POST", "/v1/task-service-credentials", ownerToken, map[string]any{"name": "fixture-service", "profileIds": []string{profileID}})
	if response.Code != 201 {
		t.Fatalf("credential: %d %s", response.Code, response.Body.String())
	}
	var credential struct{ ID, Token string }
	json.Unmarshal(response.Body.Bytes(), &credential)
	catalog := taskHTTP(srv, "GET", "/v1/tasks/profiles", credential.Token, nil)
	if catalog.Code != 200 || !strings.Contains(catalog.Body.String(), profileID) || strings.Contains(catalog.Body.String(), "private@example.test") || strings.Contains(catalog.Body.String(), "fixture") {
		t.Fatalf("catalog leak: %d %s", catalog.Code, catalog.Body.String())
	}
	if response = taskHTTP(srv, "GET", "/v1/login-profiles", credential.Token, nil); response.Code != 401 {
		t.Fatalf("service accessed owner API: %d", response.Code)
	}
	if response = taskHTTP(srv, "GET", "/v1/task-service-credentials/profiles", credential.Token, nil); response.Code != 401 {
		t.Fatalf("service accessed profile mapping: %d", response.Code)
	}
	response = taskHTTP(srv, "POST", "/v1/tasks", credential.Token, testGoalRequest("fixture-service-task"))
	if response.Code != 201 {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	var task v1.GoalTask
	json.Unmarshal(response.Body.Bytes(), &task)
	if task.Creator.Kind != "service" || task.Box != nil || task.State != "queued" {
		t.Fatalf("task: %+v", task)
	}
	a := goalTaskActor{Principal: p, Kind: "owner", ID: p.UserID}
	other, _, err := store.createGoalTask(ctx, a, testGoalRequest("fixture-owner"), "owner-task")
	if err != nil {
		t.Fatal(err)
	}
	if response = taskHTTP(srv, "GET", "/v1/tasks/"+other, credential.Token, nil); response.Code != 404 {
		t.Fatalf("read another creator: %d", response.Code)
	}
	for _, kind := range []string{"control", "view"} {
		response = taskHTTP(srv, "POST", "/v1/tasks/"+task.ID+"/links", credential.Token, map[string]any{"kinds": []string{kind}})
		if kind == "control" && response.Code != 403 {
			t.Fatal("control permission bypass", response.Code)
		}
		if kind == "view" && response.Code != 201 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	links, err := srv.mintGoalTaskLinks(ctx, task, v1.TaskLinkRequest{Kinds: []string{"view"}})
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.TrimPrefix(links[0].URL, srv.PublicURL+"/t/")
	exchange := taskHTTP(srv, "GET", "/t/"+raw, "", nil)
	if exchange.Code != 303 || strings.Contains(exchange.Header().Get("Location"), raw) {
		t.Fatalf("exchange: %d %s", exchange.Code, exchange.Body.String())
	}
	cookies := exchange.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/v1/task-links/" {
		t.Fatal("unsafe cookie")
	}
	r := httptest.NewRequest("GET", "/v1/task-links/state?link="+links[0].ID, nil)
	r.AddCookie(cookies[0])
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), task.Goal) {
		t.Fatalf("link state: %d %s", w.Code, w.Body.String())
	}
	hash := sha256.Sum256([]byte(raw))
	var saved []byte
	store.DB.QueryRowContext(ctx, `SELECT token_hash FROM goal_task_links WHERE id=$1`, links[0].ID).Scan(&saved)
	if !bytes.Equal(saved, hash[:]) {
		t.Fatal("token not hashed")
	}
	response = taskHTTP(srv, "DELETE", "/v1/tasks/"+task.ID+"/links", credential.Token, nil)
	if response.Code != 204 {
		t.Fatal(response.Code)
	}
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("revoked session still usable", w.Code)
	}
	response = taskHTTP(srv, "POST", "/v1/tasks/"+task.ID+"/cancel", credential.Token, map[string]any{})
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = taskHTTP(srv, "POST", "/v1/tasks/"+task.ID+"/messages", credential.Token, map[string]string{"text": "wake"})
	if response.Code != 409 {
		t.Fatal("terminal task message accepted", response.Code)
	}
	// A service cannot cross account boundaries even when it knows an ID.
	q, err := store.Bootstrap(ctx, "other-account", "other", uuid())
	if err != nil {
		t.Fatal(err)
	}
	third, _, err := store.createGoalTask(ctx, goalTaskActor{Principal: q, Kind: "owner", ID: q.UserID}, testGoalRequest("fixture-other"), "other")
	if err != nil {
		t.Fatal(err)
	}
	response = taskHTTP(srv, "GET", "/v1/tasks/"+third, ownerToken, nil)
	if response.Code != 404 {
		t.Fatal("account isolation failed", response.Code)
	}
}

func TestGoalTaskFinishPreventsWakeAndPreservesTranscriptPostgres(t *testing.T) {
	store, p, _ := goalTaskTestStore(t)
	ctx := context.Background()
	a := goalTaskActor{Principal: p, Kind: "owner", ID: p.UserID}
	id, _, err := store.createGoalTask(ctx, a, testGoalRequest("fixture-final"), "final")
	if err != nil {
		t.Fatal(err)
	}
	boxID, sessionID := uuid(), uuid()
	if _, err = store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,'fixture-final-box','shared-worker','running','fixture-volume','fixture-volume')`, boxID, p.AccountID, p.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.ExecContext(ctx, `INSERT INTO box_tasks(id,account_id,user_id,logical_box_id,requested_role,agent,session_name,prompt,state,idempotency_key) VALUES($1,$2,$3,$4,'owner','codex','fixture-session','fixture','active','fixture-session')`, sessionID, p.AccountID, p.UserID, boxID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.ExecContext(ctx, `INSERT INTO box_messages(id,account_id,task_id,user_id,direction,body,state,idempotency_key) VALUES($1,$2,$3,$4,'agent','fixture transcript','delivered','fixture-message')`, uuid(), p.AccountID, sessionID, p.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.ExecContext(ctx, `UPDATE goal_tasks SET logical_box_id=$2,state='running',slot_held=true WHERE id=$1`, id, boxID); err != nil {
		t.Fatal(err)
	}
	transcript, err := store.goalTaskTranscript(ctx, id)
	if err != nil || !strings.Contains(string(transcript), "fixture transcript") {
		t.Fatal(string(transcript), err)
	}
	if _, err = store.finishGoalTask(ctx, p.AccountID, id, "completed", "done", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='hibernated' WHERE id=$1`, boxID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='reserved' WHERE id=$1`, boxID); err == nil {
		t.Fatal("finished task woke through ordinary allocation")
	}
	if _, _, err = store.CreateBoxMessage(ctx, p, sessionID, "late-message", v1.SendBoxMessageRequest{Text: "wake"}); err == nil {
		t.Fatal("finished task accepted chat")
	}
	if _, err = store.DB.ExecContext(ctx, `UPDATE goal_tasks SET transcript=$2,captured_at=now() WHERE id=$1`, id, []byte(transcript)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.ExecContext(ctx, `DELETE FROM logical_boxes WHERE id=$1`, boxID); err != nil {
		t.Fatal(err)
	}
	saved, err := store.goalTaskTranscript(ctx, id)
	if err != nil || string(saved) != string(transcript) {
		t.Fatal("lost transcript", string(saved), err)
	}
}

func TestGoalTaskCreationBindsInstructionsAtomicallyPostgres(t *testing.T) {
	store, p, _ := goalTaskTestStore(t)
	ctx := context.Background()
	store.agentCLILatestResolve = func(context.Context, string) (string, error) { return "1.0.0", nil }
	a := goalTaskActor{Principal: p, Kind: "owner", ID: p.UserID}
	req := testGoalRequest("fixture-start")
	req.Instructions = "Fixture custom instructions"
	id, _, err := store.createGoalTask(ctx, a, req, "start")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.claimGoalTask(ctx, p.AccountID, id); err != nil || !ok {
		t.Fatal(ok, err)
	}
	slotID := uuid()
	for _, query := range []string{
		`INSERT INTO provider_credentials(id,account_id,provider,name,encrypted_value) VALUES(gen_random_uuid(),$1,'shared-worker','fixture-pool','fixture')`,
		`INSERT INTO fleet_settings(account_id,provider,provider_credential,compute_box_slots) VALUES($1,'shared-worker','fixture-pool',1)`,
	} {
		if _, err = store.DB.ExecContext(ctx, query, p.AccountID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,provider_credential,ordinal,state,health) VALUES($1,$2,'shared-worker','fixture-pool',1,'free','healthy')`, slotID, p.AccountID); err != nil {
		t.Fatal(err)
	}
	create := v1.CreateLogicalBoxRequest{GoalTaskID: id, Name: "fixture-start-box", Provider: "shared-worker", ProviderCredential: "fixture-pool", DefaultAgent: "codex", DiskGiB: 10, Tools: req.Tools}
	creation, err := store.BeginLogicalBoxCreation(ctx, p, create)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.goalTask(ctx, a, id)
	if err != nil || task.Box == nil || task.Box.ID != creation.Assignment.Box.ID {
		t.Fatal(task, err)
	}
	snap, err := store.InstructionSnapshot(ctx, p, task.Box.ID)
	if err != nil || !strings.Contains(snap.Markdown, "completed_task") || !strings.Contains(snap.Markdown, req.Instructions) {
		t.Fatal("missing atomic instructions", snap, err)
	}
	var budget int
	if err = store.DB.QueryRowContext(ctx, `SELECT (metadata->>'runBudgetSeconds')::integer FROM logical_boxes WHERE id=$1`, task.Box.ID).Scan(&budget); err != nil || budget != 0 {
		t.Fatal("task ordinary run budget not disabled", budget, err)
	}
}

func TestGoalTaskCreatorNoticeWaitsForLiveSessionPostgres(t *testing.T) {
	store, p, _ := goalTaskTestStore(t)
	ctx := context.Background()
	creator, session := uuid(), uuid()
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,'fixture-creator','shared-worker','hibernated','creator-volume','creator-volume')`, creator, p.AccountID, p.UserID); err != nil {
		t.Fatal(err)
	}
	notice := v1.GoalTask{ID: uuid(), Creator: v1.GoalTaskCreator{Kind: "box", ID: creator}}
	if err := store.queueGoalTaskNotice(ctx, p, notice, "[Task completed] fixture: done"); err != nil {
		t.Fatal(err)
	}
	if err := store.deliverGoalTaskNotices(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	store.DB.QueryRowContext(ctx, `SELECT count(*) FROM goal_task_notices`).Scan(&count)
	if count != 1 {
		t.Fatal("notice lost before session was available")
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO box_tasks(id,account_id,user_id,logical_box_id,requested_role,agent,session_name,prompt,state,idempotency_key) VALUES($1,$2,$3,$4,'owner','codex','creator-session','fixture','active','creator-session')`, session, p.AccountID, p.UserID, creator); err != nil {
		t.Fatal(err)
	}
	if err := store.deliverGoalTaskNotices(ctx); err != nil {
		t.Fatal(err)
	}
	store.DB.QueryRowContext(ctx, `SELECT count(*) FROM box_messages`).Scan(&count)
	if count != 0 {
		t.Fatal("notification was delivered into a sleeping workspace")
	}
	if _, err := store.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='running' WHERE id=$1`, creator); err != nil {
		t.Fatal(err)
	}
	if err := store.deliverGoalTaskNotices(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.deliverGoalTaskNotices(ctx); err != nil {
		t.Fatal(err)
	}
	store.DB.QueryRowContext(ctx, `SELECT count(*) FROM box_messages WHERE task_id=$1 AND body='[Task completed] fixture: done'`, session).Scan(&count)
	if count != 1 {
		t.Fatal("notice not delivered exactly once", count)
	}
	store.DB.QueryRowContext(ctx, `SELECT count(*) FROM goal_task_notices`).Scan(&count)
	if count != 0 {
		t.Fatal("delivered notice retained")
	}
}
