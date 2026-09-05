package notifications

import (
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
