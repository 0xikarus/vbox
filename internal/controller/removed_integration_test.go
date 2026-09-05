package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestRemovedTelegramIntegrationIsUnavailable(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest(http.MethodPost, "/v1/integrations/telegram/account/name", nil)
	r.SetPathValue("kind", "telegram")
	w := httptest.NewRecorder()
	s.notificationInbound(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("removed callback status = %d", w.Code)
	}
	if _, err := (&Store{}).PutNotification(context.Background(), Principal{}, "telegram", "removed", v1.PutNotificationRequest{}); err == nil {
		t.Fatal("removed destination kind accepted")
	}
	for _, tool := range coworkerTools() {
		if tool.(map[string]any)["name"] == "reply_owner" {
			t.Fatal("Telegram reply tool still advertised")
		}
	}
}
