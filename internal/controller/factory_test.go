package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFactoryGatewayReplacesUntrustedIdentity(t *testing.T) {
	called := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Authorization") != "Bearer gateway-test" || r.Header.Get("X-Vmbox-Account") != "account" || r.Header.Get("X-Vmbox-User") != "user" {
			t.Error("trusted identity not installed")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Forwarded-Host") != "" {
			t.Error("browser identity leaked")
		}
		w.Header().Set("Set-Cookie", "untrusted=1")
		w.WriteHeader(204)
	}))
	defer backend.Close()
	s := &Server{FactoryURL: backend.URL, FactoryToken: "gateway-test"}
	r := httptest.NewRequest("GET", "http://controller/v1/factory/work-items", nil)
	r.Header.Set("Authorization", "Bearer browser-test")
	r.Header.Set("X-Vmbox-Account", "victim")
	r.Header.Set("Cookie", "test=1")
	r.Header.Set("X-Forwarded-Host", "evil")
	w := httptest.NewRecorder()
	s.factoryGateway(w, r, Principal{AccountID: "account", UserID: "user", Role: "owner"})
	if !called || w.Code != 204 || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("unsafe response: status=%d called=%v", w.Code, called)
	}
}

func TestFactoryGatewayDoesNotGrantNativeAuthority(t *testing.T) {
	s := &Server{FactoryURL: "http://127.0.0.1:1", FactoryToken: "test"}
	w := httptest.NewRecorder()
	s.factoryGateway(w, httptest.NewRequest("POST", "/v1/factory/work-items", nil), Principal{Role: "user"})
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	s = &Server{}
	w = httptest.NewRecorder()
	s.factoryGateway(w, httptest.NewRequest("GET", "/v1/factory/capabilities", nil), Principal{Role: "owner"})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
