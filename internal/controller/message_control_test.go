package controller

import (
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestBoxMessageControlDecodesWithPlainTextFallback(t *testing.T) {
	first := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	control := v1.BoxMessageControl{Kind: "remote_control", ActorBoxID: "box-a", ActorName: "Manager", Actions: 3, FirstAt: first, LastAt: first.Add(time.Minute)}
	message := v1.BoxMessage{Direction: "system", Text: encodeBoxMessageControl(control)}
	decodeBoxMessageControl(&message)
	if message.Control == nil || message.Control.Actions != 3 || message.Text != "Controlled by Manager · 3 actions" {
		t.Fatalf("decoded control=%+v text=%q", message.Control, message.Text)
	}
	message = v1.BoxMessage{Text: boxMessageControlPrefix + strings.Repeat("x", 4097)}
	decodeBoxMessageControl(&message)
	if message.Control != nil || message.Text != "Remote control notice unavailable." {
		t.Fatalf("invalid notice control=%+v text=%q", message.Control, message.Text)
	}
}
