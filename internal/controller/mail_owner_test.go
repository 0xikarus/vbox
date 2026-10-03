package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMailOwnerRoutesAreUnavailableWithoutDomain(t *testing.T) {
	t.Setenv("VMBOX_MAIL_DOMAIN", "")
	s := &Server{}
	for _, path := range []string{"/v1/mail/approvals", "/v1/mail/messages", "/v1/mail/messages/message-id", "/v1/mail/outbox", "/v1/mail/summary", "/v1/mail/addresses", "/v1/mail/addresses/address-id", "/v1/mail/settings", "/v1/logical-boxes/box-1/mail", "/v1/logical-boxes/box-1/mail/messages", "/v1/logical-boxes/box-1/mail/outbox/draft-1"} {
		t.Run(path, func(t *testing.T) {
			called := false
			wrapped := s.mailConfigured(func(http.ResponseWriter, *http.Request, Principal) { called = true })
			response := httptest.NewRecorder()
			wrapped(response, httptest.NewRequest(http.MethodGet, path, nil), Principal{Role: "owner"})
			if called || response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "mail is not configured") {
				t.Fatalf("feature-off response=%d %q; backend called=%t", response.Code, response.Body.String(), called)
			}
		})
	}
}

func TestOwnerMailOutboxItemGETIsRegistered(t *testing.T) {
	t.Setenv("VMBOX_MAIL_DOMAIN", "example.test")
	response := httptest.NewRecorder()
	NewServer(nil, nil).Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/logical-boxes/box-1/mail/outbox/draft-1", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("GET outbox item route is not registered: %d %s", response.Code, response.Body.String())
	}
}
