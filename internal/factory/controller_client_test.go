package factory

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestControllerBindingRevalidatesAuthority(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"accountId":"a","role":"user"}`)) }))
	defer upstream.Close()
	c := &ControllerClient{URL: upstream.URL, Token: "test-token", AccountID: "a"}
	if c.Authorize(context.Background(), "a") == nil {
		t.Fatal("revoked owner authority accepted")
	}
}
