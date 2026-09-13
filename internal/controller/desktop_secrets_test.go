package controller

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecretOriginRequiresExactHTTPSOrigin(t *testing.T) {
	for _, input := range []string{"http://example.com", "https://example.com/login", "https://user:pass@example.com", "https://example.com?token=x", "file:///tmp/password"} {
		if _, err := secretOrigin(input); err == nil {
			t.Fatal("accepted non-origin destination")
		}
	}
	if origin, err := secretOrigin("https://EXAMPLE.com/"); err != nil || origin != "https://example.com" {
		t.Fatal("origin normalization failed")
	}
}

func TestSecretAPIRejectsAmbiguousGenerationWithoutEchoingValue(t *testing.T) {
	server := NewServer(nil, nil)
	for _, body := range []string{`{"key":"test","origin":"https://example.com","value":"synthetic-private-value","generate":true}`, `{"key":"test","origin":"https://example.com","generate":false}`, `{"generate":true,"unexpected":"synthetic-private-value"}`} {
		r := httptest.NewRequest("POST", "/secrets", strings.NewReader(body))
		w := httptest.NewRecorder()
		server.desktopSecrets(w, r, Principal{})
		if w.Code != 400 || strings.Contains(w.Body.String(), "synthetic-private-value") {
			t.Fatal("invalid request accepted or echoed")
		}
	}
	data, err := json.Marshal(DesktopSecret{Key: "reference", Origin: "https://example.com", Status: "pending"})
	if err != nil || strings.Contains(string(data), "encrypted") || strings.Contains(string(data), "value") {
		t.Fatal("secret metadata contains value fields")
	}
}

func TestEnsureSecretRejectsExistingCredentialAndPrivateValues(t *testing.T) {
	server := NewServer(nil, nil)
	for _, body := range []string{`{}`, `{"purpose":"existing_login"}`, `{"purpose":"new_account_password","value":"synthetic-private"}`, `{"purpose":"new_account_password"} {}`} {
		request := httptest.NewRequest("POST", "/ensure", strings.NewReader(body))
		response := httptest.NewRecorder()
		server.ensureAgentDesktopSecret(response, request, Principal{})
		if response.Code != 400 || strings.Contains(response.Body.String(), "synthetic-private") {
			t.Fatal("invalid secret generation request accepted or echoed")
		}
	}
}

func TestEnsureSecretRejectsInvalidPasswordPolicyBeforeResolution(t *testing.T) {
	server := NewServer(nil, nil)
	for _, body := range []string{
		`{"purpose":"new_account_password","length":8}`,
		`{"purpose":"new_account_password","length":129}`,
		`{"purpose":"new_account_password","alphabet":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		`{"purpose":"new_account_password","alphabet":"0123456789"}`,
	} {
		request := httptest.NewRequest("POST", "/ensure", strings.NewReader(body))
		response := httptest.NewRecorder()
		server.ensureAgentDesktopSecret(response, request, Principal{})
		if response.Code != 400 {
			t.Fatal("invalid policy reached secret resolution")
		}
	}
}
