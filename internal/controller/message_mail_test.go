package controller

import (
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestBoxMessageMailEnvelopeDecodesForClients(t *testing.T) {
	for _, tc := range []struct {
		mail v1.BoxMessageMail
		want string
	}{
		{v1.BoxMessageMail{Kind: "mail_batch", Items: []v1.BoxMessageMailItem{{ID: "one", From: "outside@example.test", Subject: "Hello"}, {ID: "two", Subject: "World"}}, More: 3}, "✉ 2 new mails · 3 more"},
		{v1.BoxMessageMail{Kind: "outbox_status", OutboxID: "draft-1", To: "owner@example.test", Status: "rejected", Reason: "Revise it"}, "Draft rejected"},
	} {
		message := v1.BoxMessage{Direction: "system", Text: encodeBoxMessageMail(tc.mail)}
		decodeBoxMessageMail(&message)
		if message.Text != tc.want || message.Mail == nil || message.Mail.Kind != tc.mail.Kind {
			t.Fatalf("decoded mail = %+v, want text %q and kind %q", message, tc.want, tc.mail.Kind)
		}
	}
	for _, invalid := range []string{boxMessageMailPrefix + "?", encodeBoxMessageMail(v1.BoxMessageMail{Kind: "mail_batch"}), encodeBoxMessageMail(v1.BoxMessageMail{Kind: "outbox_status", OutboxID: "x", Status: "approved"})} {
		message := v1.BoxMessage{Text: invalid}
		decodeBoxMessageMail(&message)
		if message.Mail != nil || strings.Contains(message.Text, boxMessageMailPrefix) {
			t.Fatalf("invalid envelope leaked: %+v", message)
		}
	}
}
