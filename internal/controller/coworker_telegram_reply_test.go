package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

type telegramReplyTransport func(*http.Request) (*http.Response, error)

func (f telegramReplyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testTelegramCorrelatedReply(t *testing.T, ctx context.Context, s *Server, owner Principal, box, sibling string) {
	config, _ := json.Marshal(map[string]any{"chatId": 9, "userMap": map[string]string{"7": owner.UserID}})
	_, err := s.Store.PutNotification(ctx, owner, "telegram", "reply-test", v1.PutNotificationRequest{Config: config, Secret: json.RawMessage(`{"token":"synthetic","webhookSecret":"synthetic"}`), AllowedUsers: []string{"7"}, AllowedChats: []string{"9"}})
	if err != nil {
		t.Fatal(err)
	}
	seq, err := s.Store.sendOwnerCoworkerMessage(ctx, owner, box, "telegram-reply-test", "Hello", &telegramReplyRoute{Destination: "reply-test", ChatID: 9})
	if err != nil {
		t.Fatal(err)
	}
	sends := 0
	previous := s.HTTP
	defer func() { s.HTTP = previous }()
	s.HTTP = &http.Client{Transport: telegramReplyTransport(func(r *http.Request) (*http.Response, error) {
		sends++
		var body struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.ChatID != 9 || !strings.Contains(body.Text, "actual test response") {
			t.Error("wrong reply destination/body")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})}
	id := CoworkerIdentity{AccountID: owner.AccountID, BoxID: box}
	if _, err = s.replyTelegramOwner(ctx, CoworkerIdentity{AccountID: owner.AccountID, BoxID: sibling}, seq, "actual test response"); err == nil {
		t.Fatal("sibling could reply")
	}
	if _, err = s.replyTelegramOwner(ctx, id, seq, "actual test response"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.replyTelegramOwner(ctx, id, seq, "actual test response"); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatal("reply duplicated", sends)
	}
	response := httptest.NewRecorder()
	s.coworkerOwnerMessages(response, httptest.NewRequest(http.MethodGet, "/v1/coworkers/messages", nil).WithContext(ctx), owner)
	var messages []struct {
		Sequence   int64  `json:"sequence"`
		ReplyState string `json:"replyState"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &messages) != nil {
		t.Fatal("could not read owner delivery status", response.Code)
	}
	found := false
	for _, message := range messages {
		if message.Sequence == seq {
			found = true
			if message.ReplyState != "sent" {
				t.Fatalf("reply state = %q, want sent", message.ReplyState)
			}
		}
	}
	if !found {
		t.Fatal("reply event missing from owner messages")
	}
	if _, err = s.replyTelegramOwner(ctx, id, seq, "changed response"); err == nil {
		t.Fatal("changed retry accepted")
	}
	if _, err = s.Store.DB.ExecContext(ctx, `UPDATE coworker_telegram_replies SET state='uncertain' WHERE event_sequence=$1`, seq); err != nil {
		t.Fatal(err)
	}
	if _, err = s.replyTelegramOwner(ctx, id, seq, "actual test response"); err == nil || sends != 1 {
		t.Fatal("uncertain send replayed")
	}
}
