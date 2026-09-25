package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Called by the opt-in PostgreSQL suite with a disposable hibernated box.
func testDesktopPrivateDataRoutes(t *testing.T, store *Store, owner, other Principal, box string) {
	t.Helper()
	server := NewServer(store, nil)
	invoke := func(handler func(http.ResponseWriter, *http.Request, Principal), method, body, key string, p Principal) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/", strings.NewReader(body))
		request.SetPathValue("id", box)
		request.SetPathValue("key", key)
		response := httptest.NewRecorder()
		handler(response, request, p)
		return response
	}
	if _, err := store.DB.Exec(`UPDATE logical_boxes SET metadata=metadata || '{"importedLoginProfiles":[{"application":"codex","name":"work"}],"setupScript":"private-setup-fixture"}'::jsonb WHERE id=$1`, box); err != nil {
		t.Fatal(err)
	}
	refs := invoke(server.importedCredentials, "GET", "", "", owner)
	if refs.Code != 200 || !strings.Contains(refs.Body.String(), `"name":"work"`) || !strings.Contains(refs.Body.String(), `"verified":true`) || strings.Contains(refs.Body.String(), "private-setup-fixture") {
		t.Fatal("credential reference projection failed")
	}
	if refs = invoke(server.importedCredentials, "GET", "", "", other); refs.Code != 404 {
		t.Fatal("cross-account credential references exposed")
	}
	for _, key := range []string{"private-login", "cancel-login"} {
		if _, err := store.DB.Exec(`INSERT INTO desktop_secret_requests(account_id,box_id,secret_key,origin) VALUES($1,$2,$3,'https://example.com')`, owner.AccountID, box, key); err != nil {
			t.Fatal(err)
		}
	}
	response := invoke(server.desktopIdlePolicy, "GET", "", "", owner)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "14400") {
		t.Fatal("new box idle default missing")
	}
	response = invoke(server.desktopIdlePolicy, "PUT", `{"seconds":0}`, "", owner)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"resumeSeconds":14400`) {
		t.Fatal("cannot disable inactivity")
	}
	response = invoke(server.desktopIdlePolicy, "PUT", `{"seconds":21600}`, "", owner)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"resumeSeconds":21600`) {
		t.Fatal("cannot set inactivity interval")
	}
	response = invoke(server.desktopIdlePolicy, "PUT", `{"seconds":0}`, "", owner)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"resumeSeconds":21600`) {
		t.Fatal("disabled policy did not retain the chosen interval")
	}
	response = invoke(server.desktopIdlePolicy, "PUT", `{"seconds":60}`, "", other)
	if response.Code != 404 {
		t.Fatal("cross-account idle policy accepted")
	}
	value := "synthetic-private-fixture-only"
	response = invoke(server.desktopSecretRequests, "POST", `{"value":"`+value+`"}`, "private-login", other)
	if response.Code != 404 {
		t.Fatalf("cross-account private submit: %d", response.Code)
	}
	response = invoke(server.desktopSecretRequests, "POST", `{"value":"`+value+`"}`, "private-login", owner)
	if response.Code != 200 || strings.Contains(response.Body.String(), value) || !strings.Contains(response.Body.String(), "fulfilled") {
		t.Fatal("private submission failed or exposed value")
	}
	response = invoke(server.desktopSecretRequests, "POST", `{"cancel":true}`, "cancel-login", owner)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "cancelled") {
		t.Fatal("private cancellation failed")
	}
	var cipher string
	if err := store.DB.QueryRow(`SELECT encrypted_value FROM desktop_secrets WHERE box_id=$1 AND secret_key='private-login'`, box).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	plain, err := store.Envelope.Open(desktopSecretScope(owner.AccountID, box, "private-login", "https://example.com"), cipher)
	if err != nil || string(plain) != value {
		t.Fatal("private password not durably saved")
	}
	clear(plain)
	response = invoke(server.boxMessageHistory, "GET", "", "", owner)
	if response.Code != 200 || strings.Contains(response.Body.String(), value) {
		t.Fatal("private password in history or history query failed")
	}
	testBoxMessageHistoryReturnsMessages(t, store, server, invoke, owner, box)
	response = invoke(server.deleteDesktopSecret, "DELETE", "", "private-login", other)
	if response.Code != 404 {
		t.Fatal("cross-account secret deletion accepted")
	}
	var remaining int
	if err := store.DB.QueryRow(`SELECT count(*) FROM desktop_secrets WHERE box_id=$1 AND secret_key='private-login'`, box).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatal("foreign deletion changed the credential")
	}
	response = invoke(server.deleteDesktopSecret, "DELETE", "", "private-login", owner)
	if response.Code != 204 {
		t.Fatalf("secret deletion failed: %d", response.Code)
	}
	if err := store.DB.QueryRow(`SELECT count(*) FROM desktop_secret_requests WHERE box_id=$1 AND secret_key='private-login'`, box).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("deleted credential still has a fulfilled request")
	}
	if _, err := store.DB.Exec(`INSERT INTO desktop_secret_requests(account_id,box_id,secret_key,origin) VALUES($1,$2,'private-login','https://example.com')`, owner.AccountID, box); err != nil {
		t.Fatal("cannot request deleted reference again", err)
	}
	response = invoke(server.desktopSecretRequests, "POST", `{"value":"replacement-fixture-only"}`, "private-login", owner)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "fulfilled") {
		t.Fatal("cannot fulfill replacement request")
	}
	response = invoke(server.desktopScreenshot, "GET", "", "", owner)
	if response.Code != 409 {
		t.Fatalf("sleeping screenshot status %d", response.Code)
	}
	response = invoke(server.desktopScreenshot, "GET", "", "", other)
	if response.Code != 404 {
		t.Fatal("cross-account screenshot accepted")
	}
	state := `{"version":1,"origins":[{"origin":"https://example.com","localStorage":[{"name":"session","value":"synthetic-state-only"}]}]}`
	response = invoke(server.browserStateImports, "POST", state, "", owner)
	if response.Code != 201 || strings.Contains(response.Body.String(), "synthetic-state-only") {
		t.Fatal("private browser import failed or exposed state")
	}
	var saved struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(response.Body.Bytes(), &saved) != nil || saved.ID == "" {
		t.Fatal("missing import reference")
	}
	if err := store.DB.QueryRow(`SELECT encrypted_value FROM browser_state_imports WHERE id=$1`, saved.ID).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	plain, err = store.Envelope.Open(browserStateScope(owner.AccountID, box, saved.ID), cipher)
	if err != nil || string(plain) != state {
		t.Fatal("browser state encryption failed")
	}
	clear(plain)
	response = invoke(server.browserStateImports, "GET", "", "", other)
	if response.Code != 404 {
		t.Fatal("cross-account browser import listing accepted")
	}
	ctx := context.Background()
	if _, err := store.DB.Exec(`UPDATE logical_boxes SET state='running',idle_timeout_seconds=60,updated_at=now()-interval '5 minutes' WHERE id=$1`, box); err != nil {
		t.Fatal(err)
	}
	eligible := func(want bool) {
		t.Helper()
		seconds, got, err := desktopIdleEligible(ctx, store.DB, box, owner.AccountID)
		if err != nil || got != want || seconds != 60 {
			t.Fatalf("idle eligibility got %t want %t: %v", got, want, err)
		}
	}
	eligible(true)
	task := uuid()
	if _, err := store.DB.Exec(`INSERT INTO box_tasks(id,account_id,logical_box_id,user_id,requested_role,agent,prompt,state,idempotency_key) VALUES($1,$2,$3,$4,'user','codex','synthetic idle test','active',$5)`, task, owner.AccountID, box, owner.UserID, task); err != nil {
		t.Fatal(err)
	}
	eligible(false)
	if _, err := store.DB.Exec(`UPDATE box_tasks SET state='cancelled' WHERE id=$1`, task); err != nil {
		t.Fatal(err)
	}
	eligible(true)
	if _, err := store.DB.Exec(`UPDATE desktop_secret_requests SET status='pending' WHERE box_id=$1 AND secret_key='cancel-login'`, box); err != nil {
		t.Fatal(err)
	}
	eligible(false)
	if _, err := store.DB.Exec(`UPDATE desktop_secret_requests SET status='cancelled' WHERE box_id=$1 AND secret_key='cancel-login'`, box); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`UPDATE logical_boxes SET updated_at=now() WHERE id=$1`, box); err != nil {
		t.Fatal(err)
	}
	eligible(false)
	if _, err := store.DB.Exec(`UPDATE logical_boxes SET state='hibernated' WHERE id=$1`, box); err != nil {
		t.Fatal(err)
	}

}

// An empty history hides a column mismatch between the query and the scan, so
// the chat UI only broke once a box had said something. One real message is
// enough to catch it.
func testBoxMessageHistoryReturnsMessages(t *testing.T, store *Store, server *Server, invoke func(func(http.ResponseWriter, *http.Request, Principal), string, string, string, Principal) *httptest.ResponseRecorder, owner Principal, box string) {
	t.Helper()
	task := uuid()
	if _, err := store.DB.Exec(`INSERT INTO box_tasks(id,account_id,logical_box_id,user_id,requested_role,agent,prompt,state,idempotency_key)
		VALUES($1,$2,$3,$4,'owner','codex','history fixture','active',$5)`, task, owner.AccountID, box, owner.UserID, "history-fixture-"+task); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Truncate(time.Second)
	for index, message := range []struct{ direction, body string }{{"user", "history-question"}, {"agent", "history-answer"}, {"user", "history-followup"}, {"agent", "history-result"}} {
		if _, err := store.DB.Exec(`INSERT INTO box_messages(id,account_id,task_id,direction,body,state,idempotency_key,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,'delivered',$6,$7,$7)`, uuid(), owner.AccountID, task, message.direction, message.body, message.direction+"-"+task+"-"+message.body, stamp.Add(time.Duration(index/2)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	response := invoke(server.boxMessageHistory, "GET", "", "", owner)
	if response.Code != 200 {
		t.Fatalf("history with messages failed: %d %s", response.Code, response.Body.String())
	}
	for _, want := range []string{"history-question", "history-answer"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("history lost %q: %s", want, response.Body.String())
		}
	}
	page := func(query string) []struct {
		ID        string    `json:"id"`
		Text      string    `json:"text"`
		CreatedAt time.Time `json:"createdAt"`
	} {
		request := httptest.NewRequest("GET", "/?"+query, nil)
		request.SetPathValue("id", box)
		response := httptest.NewRecorder()
		server.boxMessageHistory(response, request, owner)
		if response.Code != 200 {
			t.Fatalf("paged history failed: %d %s", response.Code, response.Body.String())
		}
		var values []struct {
			ID        string    `json:"id"`
			Text      string    `json:"text"`
			CreatedAt time.Time `json:"createdAt"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &values); err != nil {
			t.Fatal(err)
		}
		return values
	}
	newest := page("limit=2")
	if len(newest) != 2 {
		t.Fatalf("expected bounded newest page, got %d messages", len(newest))
	}
	older := page("limit=2&before=" + url.QueryEscape(newest[0].CreatedAt.Format(time.RFC3339Nano)) + "&beforeId=" + url.QueryEscape(newest[0].ID))
	if len(older) != 2 {
		t.Fatalf("expected two older messages, got %d", len(older))
	}
	seen := map[string]bool{}
	for _, value := range append(newest, older...) {
		if seen[value.ID] {
			t.Fatal("pagination repeated a message")
		}
		seen[value.ID] = true
	}
	// The fixture task is active, which would make the box look busy to the
	// idle checks that follow.
	if _, err := store.DB.Exec(`DELETE FROM box_tasks WHERE id=$1`, task); err != nil {
		t.Fatal(err)
	}
}
