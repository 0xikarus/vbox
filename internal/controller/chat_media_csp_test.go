package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

// The chat plays attachments from same-origin URLs and local drafts from
// blob URLs, so the app CSP must allow media from both.
func TestChatCSPAllowsMedia(t *testing.T) {
	server := NewServer(nil, provider.NewRegistry())
	request := httptest.NewRequest("GET", "/chat", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "media-src 'self' blob:") {
		t.Fatalf("chat CSP is missing media-src: %q", response.Header().Get("Content-Security-Policy"))
	}
}
