package notifications

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type answerRecorder struct{ called bool }

func (a *answerRecorder) Answer(context.Context, string, string, string) error {
	a.called = true
	return nil
}

func TestWebhookSignatureAndNoSecretInPayload(t *testing.T) {
	secret := []byte("signing-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if string(data) == "" {
			t.Error("empty body")
		}
		if got := r.Header.Get("X-VMBox-Signature"); len(got) != 71 {
			t.Errorf("signature=%q", got)
		}
		if string(data) == hex.EncodeToString(secret) {
			t.Error("secret sent as payload")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	adapter := Webhook{URL: server.URL, Secret: secret, Client: server.Client()}
	if err := adapter.Send(context.Background(), Delivery{RunID: "run", Message: "done"}); err != nil {
		t.Fatal(err)
	}
}

func TestTelegramRequiresUserAndChatAllowlists(t *testing.T) {
	recorder := &answerRecorder{}
	telegram := Telegram{AllowedUsers: map[int64]bool{7: true}, AllowedChats: map[int64]bool{9: true}, Answerer: recorder}
	allowed := `{"message":{"text":"/answer q_1 yes","from":{"id":7},"chat":{"id":9}}}`
	if err := telegram.HandleUpdate(context.Background(), bytes.NewBufferString(allowed)); err != nil {
		t.Fatal(err)
	}
	if !recorder.called {
		t.Fatal("authorized answer was not delivered")
	}
	recorder.called = false
	blocked := `{"message":{"text":"/answer q_1 yes","from":{"id":7},"chat":{"id":10}}}`
	if err := telegram.HandleUpdate(context.Background(), bytes.NewBufferString(blocked)); err == nil || recorder.called {
		t.Fatalf("non-allowlisted chat accepted: %v", err)
	}
}
