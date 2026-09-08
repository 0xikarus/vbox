package factory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestControllerBindingCannotCrossAccountsOrRedirect(t *testing.T) {
	requests := 0
	leaked := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer other.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/v1/whoami" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"accountId":"a","role":"owner"}`))
			return
		}
		http.Redirect(w, r, other.URL, 302)
	}))
	defer upstream.Close()
	c := &ControllerClient{URL: upstream.URL, Token: "test-token", AccountID: "a"}
	if _, err := c.Profiles(context.Background(), "victim"); err == nil || requests != 0 {
		t.Fatal("foreign account reached controller")
	}
	if _, err := c.Profiles(context.Background(), "a"); err == nil || leaked {
		t.Fatal("redirect followed or accepted")
	}
}

func TestPlannerSubmissionCarriesOnlyFixedCommand(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/whoami" {
			w.Write([]byte(`{"accountId":"a","role":"owner"}`))
			return
		}
		calls++
		if r.URL.Path != "/v1/logical-boxes/box/process-tasks" || r.Method != "POST" || r.Header.Get("Idempotency-Key") != "factory-plan:"+strings.Repeat("b", 32) {
			t.Error("wrong submission")
		}
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid body")
		}
		if body["agent"] != "shell" || strings.Contains(body["prompt"], "token") || !strings.HasSuffix(body["prompt"], "/job.json") {
			t.Error("unexpected command")
		}
		json.NewEncoder(w).Encode(map[string]string{"id": "task", "logicalBoxId": "box", "agent": "shell", "prompt": body["prompt"]})
	}))
	defer upstream.Close()
	c := &ControllerClient{URL: upstream.URL, Token: "test-token", AccountID: "a"}
	for range 2 {
		if _, err := c.SubmitPlanner(context.Background(), "a", "box", strings.Repeat("b", 32)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.SubmitPlanner(context.Background(), "a", "box", "bad; command"); err == nil || calls != 2 {
		t.Fatal("unsafe attempt accepted")
	}
}

func TestControllerBindingRevalidatesAuthority(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"accountId":"a","role":"user"}`)) }))
	defer upstream.Close()
	c := &ControllerClient{URL: upstream.URL, Token: "test-token", AccountID: "a"}
	if c.Authorize(context.Background(), "a") == nil {
		t.Fatal("revoked owner authority accepted")
	}
}
