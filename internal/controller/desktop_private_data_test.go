package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	if response.Code != 200 {
		t.Fatal("cannot disable inactivity")
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
