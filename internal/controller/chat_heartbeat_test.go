package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestChatHeartbeatValidation(t *testing.T) {
	good := chatHeartbeatRequest{Count: 2, IntervalMinutes: 1, Message: "Check the build"}
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	for _, r := range []chatHeartbeatRequest{{Count: 0, IntervalMinutes: 1, Message: "x"}, {Count: 1001, IntervalMinutes: 1, Message: "x"}, {Count: 1, IntervalMinutes: 0, Message: "x"}, {Count: 1, IntervalMinutes: 1441, Message: "x"}, {Count: 1, IntervalMinutes: 1, Message: " \n"}, {Count: 1, IntervalMinutes: 1, Message: "/silent not a chat message"}} {
		if r.validate() == nil {
			t.Fatalf("accepted invalid request: count=%d interval=%d", r.Count, r.IntervalMinutes)
		}
	}
}

func TestChatHeartbeatPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	base, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "heartbeat_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = base.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	u, _ := url.Parse(dsn)
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
	token := uuid()
	p, err := store.Bootstrap(ctx, "heartbeat-tests", "owner", token)
	if err != nil {
		t.Fatal(err)
	}
	otherToken := uuid()
	other, err := store.Bootstrap(ctx, "heartbeat-other", "owner", otherToken)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := store.DB.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	boxID, taskID := uuid(), uuid()
	exec(`INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,default_agent) VALUES($1,$2,$3,'heartbeat-fixture','shared-worker','running','disposable-heartbeat','disposable-heartbeat','codex')`, boxID, p.AccountID, p.UserID)
	// A queued task exercises the real ordinary chat store without starting a worker.
	exec(`INSERT INTO box_tasks(id,account_id,logical_box_id,user_id,requested_role,agent,session_name,prompt,state,idempotency_key) VALUES($1,$2,$3,$4,'owner','codex','heartbeat-fixture','fixture','queued','fixture')`, taskID, p.AccountID, boxID, p.UserID)
	server := NewServer(store, nil)
	request := chatHeartbeatRequest{Count: 2, IntervalMinutes: 3, Message: "Check progress\nand report back."}
	h, err := store.startChatHeartbeat(ctx, p, boxID, "start", request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := store.startChatHeartbeat(ctx, p, boxID, "start", request)
	if err != nil || replay.ID != h.ID {
		t.Fatalf("start replay=%+v err=%v", replay, err)
	}
	changed := request
	changed.Message = "different"
	if _, err = store.startChatHeartbeat(ctx, p, boxID, "start", changed); err == nil {
		t.Fatal("accepted changed replay")
	}
	if _, err = store.startChatHeartbeat(ctx, other, boxID, "foreign", request); err == nil {
		t.Fatal("cross-account timer")
	}
	due := func(id string) { exec(`UPDATE chat_heartbeats SET next_at=now()-interval '1 second' WHERE id=$1`, id) }
	remaining := func(id string) int {
		var n int
		if err := store.DB.QueryRowContext(ctx, `SELECT remaining FROM chat_heartbeats WHERE id=$1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	messages := func() int {
		var n int
		if err := store.DB.QueryRowContext(ctx, `SELECT count(*) FROM box_messages WHERE task_id=$1`, taskID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	due(h.ID)
	exec(`UPDATE logical_boxes SET state='hibernated' WHERE id=$1`, boxID)
	if err = store.advanceChatHeartbeat(ctx, h.ID, server.routeBoxMessage); err != nil {
		t.Fatal(err)
	}
	if remaining(h.ID) != 2 || messages() != 0 {
		t.Fatal("sleeping box consumed a tick")
	}
	exec(`UPDATE logical_boxes SET state='running' WHERE id=$1`, boxID)
	// Simulate a lost response / crash after durable normal-chat insertion.
	failAfterSave := func(c context.Context, p Principal, b, key string, r v1.DirectBoxMessageRequest) (v1.DirectBoxMessageResponse, error) {
		v, e := server.routeBoxMessage(c, p, b, key, r)
		if e != nil {
			return v, e
		}
		return v, errors.New("simulated response loss")
	}
	if err = store.advanceChatHeartbeat(ctx, h.ID, failAfterSave); err == nil {
		t.Fatal("expected simulated failure")
	}
	if messages() != 1 || remaining(h.ID) != 2 {
		t.Fatal("failed delivery did not retain retry position")
	}
	if err = store.advanceChatHeartbeat(ctx, h.ID, server.routeBoxMessage); err != nil {
		t.Fatal(err)
	}
	if messages() != 1 || remaining(h.ID) != 1 {
		t.Fatal("retry duplicated message or lost count")
	}
	var text, direction, user string
	if err = store.DB.QueryRowContext(ctx, `SELECT body,direction,user_id::text FROM box_messages WHERE task_id=$1`, taskID).Scan(&text, &direction, &user); err != nil {
		t.Fatal(err)
	}
	if text != request.Message || direction != "user" || user != p.UserID {
		t.Fatal("heartbeat is not an ordinary user message")
	}
	// Two controllers must not both deliver a due tick.
	due(h.ID)
	entered, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	var advanceErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		advanceErr = store.advanceChatHeartbeat(ctx, h.ID, func(c context.Context, p Principal, b, k string, r v1.DirectBoxMessageRequest) (v1.DirectBoxMessageResponse, error) {
			close(entered)
			<-release
			return server.routeBoxMessage(c, p, b, k, r)
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = store.advanceChatHeartbeat(ctx, h.ID, server.routeBoxMessage); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if advanceErr != nil {
		t.Fatal(advanceErr)
	}
	if messages() != 2 || remaining(h.ID) != 0 {
		t.Fatal("count did not terminate exactly")
	}
	due(h.ID)
	if err = store.advanceChatHeartbeat(ctx, h.ID, server.routeBoxMessage); err != nil {
		t.Fatal(err)
	}
	if messages() != 2 {
		t.Fatal("completed timer fired")
	}
	h2, err := store.startChatHeartbeat(ctx, p, boxID, "replacement-a", request)
	if err != nil {
		t.Fatal(err)
	}
	h3, err := store.startChatHeartbeat(ctx, p, boxID, "replacement-b", request)
	if err != nil {
		t.Fatal(err)
	}
	due(h2.ID)
	if err = store.advanceChatHeartbeat(ctx, h2.ID, server.routeBoxMessage); err != nil {
		t.Fatal(err)
	}
	if messages() != 2 {
		t.Fatal("replaced timer fired")
	}
	api := func(method, path, auth string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+auth)
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		return rec
	}
	path := "/v1/logical-boxes/" + boxID
	if r := api(http.MethodGet, path+"/active-tools", otherToken); r.Code != 404 {
		t.Fatalf("foreign list status=%d", r.Code)
	}
	exec(`UPDATE users SET role='user' WHERE id=$1`, other.UserID)
	if r := api(http.MethodGet, path+"/active-tools", otherToken); r.Code != 403 {
		t.Fatalf("non-owner list status=%d", r.Code)
	}
	r := api(http.MethodGet, path+"/active-tools", token)
	if r.Code != 200 {
		t.Fatalf("list status=%d", r.Code)
	}
	var list struct {
		Heartbeats []chatHeartbeat `json:"heartbeats"`
	}
	if err = json.Unmarshal(r.Body.Bytes(), &list); err != nil || len(list.Heartbeats) != 1 || list.Heartbeats[0].ID != h3.ID {
		t.Fatal("wrong active tools")
	}
	// A stale Stop request cannot stop the replacement.
	if r = api(http.MethodDelete, path+"/heartbeats/"+h2.ID, token); r.Code != 204 {
		t.Fatalf("stop status=%d", r.Code)
	}
	due(h3.ID)
	exec(`UPDATE users SET disabled_at=now() WHERE id=$1`, p.UserID)
	if err = store.advanceChatHeartbeat(ctx, h3.ID, server.routeBoxMessage); err != nil {
		t.Fatal(err)
	}
	if messages() != 2 {
		t.Fatal("disabled owner delivered heartbeat")
	}
	exec(`UPDATE users SET disabled_at=NULL WHERE id=$1`, p.UserID)
	if r = api(http.MethodDelete, path+"/heartbeats/"+h3.ID, token); r.Code != 204 {
		t.Fatalf("stop status=%d", r.Code)
	}
	if err = store.advanceChatHeartbeat(ctx, h3.ID, server.routeBoxMessage); err != nil {
		t.Fatal(err)
	}
	if messages() != 2 {
		t.Fatal("stopped timer fired")
	}
}
