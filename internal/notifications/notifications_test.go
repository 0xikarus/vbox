package notifications

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type answerRecorder struct{ called bool }

func (a *answerRecorder) Answer(context.Context, string, string, string, string) error {
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
	telegram := Telegram{AllowedUsers: map[int64]bool{7: true}, AllowedChats: map[int64]bool{9: true}, Answerer: recorder, UserMap: map[string]string{"7": "user-7"}}
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

func TestTelegramCoworkerCommandsRetainAllowlists(t *testing.T) {
	calls := 0
	adapter := Telegram{AllowedUsers: map[int64]bool{7: true}, AllowedChats: map[int64]bool{9: true}, UserMap: map[string]string{"7": "owner-7"}, CoworkerCommand: func(_ context.Context, user string, update, chat int64, text string) error {
		calls++
		if user != "owner-7" || update != 42 || chat != 9 {
			t.Error("identity lost")
		}
		return nil
	}}
	allowed := `{"update_id":42,"message":{"text":"/coworker coworker-test hello","from":{"id":7},"chat":{"id":9}}}`
	if err := adapter.HandleUpdate(context.Background(), bytes.NewBufferString(allowed)); err != nil || calls != 1 {
		t.Fatal("allowed coworker command failed", err)
	}
	blocked := `{"update_id":43,"message":{"text":"/coworker coworker-test hello","from":{"id":7},"chat":{"id":10}}}`
	if err := adapter.HandleUpdate(context.Background(), bytes.NewBufferString(blocked)); err == nil || calls != 1 {
		t.Fatal("allowlist bypass")
	}
}

func TestDiscordButtonOpensModalAndSubmissionAnswers(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &answerRecorder{}
	discord := Discord{
		PublicKey:       publicKey,
		AllowedUsers:    map[string]bool{"external-7": true},
		AllowedGuilds:   map[string]bool{"guild-9": true},
		AllowedChannels: map[string]bool{"channel-11": true},
		Answerer:        recorder,
		AccountID:       "account-a",
		UserMap:         map[string]string{"external-7": "user-7"},
	}
	timestamp := "123456"
	call := func(body string) (map[string]any, error) {
		signature := ed25519.Sign(privateKey, append([]byte(timestamp), []byte(body)...))
		return discord.HandleInteraction(context.Background(), hex.EncodeToString(signature), timestamp, []byte(body))
	}
	button := `{"type":3,"guild_id":"guild-9","channel_id":"channel-11","member":{"user":{"id":"external-7"}},"data":{"custom_id":"answer:q_1"}}`
	response, err := call(button)
	if err != nil || response["type"] != 9 || recorder.called {
		t.Fatalf("button response=%v called=%v err=%v", response, recorder.called, err)
	}
	modal := `{"type":5,"guild_id":"guild-9","channel_id":"channel-11","member":{"user":{"id":"external-7"}},"data":{"custom_id":"answer:q_1","components":[{"components":[{"value":"ship it"}]}]}}`
	response, err = call(modal)
	if err != nil || response["type"] != 4 || !recorder.called {
		t.Fatalf("modal response=%v called=%v err=%v", response, recorder.called, err)
	}
}
