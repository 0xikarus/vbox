package notifications

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
