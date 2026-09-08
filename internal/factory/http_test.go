package factory

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCapabilitiesExposeActualReadinessAndFormats(t *testing.T) {
	s := &Service{GatewayToken: "gateway-fixture", Images: map[string]bool{"codex": true}}
	r := httptest.NewRequest("GET", "/v1/factory/capabilities", nil)
	r.Header.Set("Authorization", "Bearer gateway-fixture")
	r.Header.Set("X-Vmbox-Account", "account")
	r.Header.Set("X-Vmbox-User", "owner")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var caps struct {
		ExecutionReady bool     `json:"executionReady"`
		ImageTypes     []string `json:"imageTypes"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &caps) != nil || caps.ExecutionReady || strings.Join(caps.ImageTypes, ",") != "image/png,image/jpeg" {
		t.Fatal("unavailable execution or image format advertised")
	}
	for _, url := range []string{"/v1/factory/work-items", "/v1/factory/work-items/work/messages"} {
		r = httptest.NewRequest("POST", url, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer gateway-fixture")
		r.Header.Set("X-Vmbox-Account", "account")
		r.Header.Set("X-Vmbox-User", "owner")
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatal("unconfigured execution accepted")
		}
	}
}

func TestFactoryRejectsMissingGatewayIdentity(t *testing.T) {
	s := &Service{GatewayToken: "gateway-fixture"}
	for _, headers := range []map[string]string{{}, {"Authorization": "Bearer gateway-fixture"}, {"Authorization": "Bearer wrong", "X-Vmbox-Account": "a", "X-Vmbox-User": "u"}} {
		r := httptest.NewRequest("GET", "/v1/factory/capabilities", nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("unauthenticated factory access accepted")
		}
	}
}
