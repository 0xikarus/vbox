package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestCoworkerGateRequiresExplicitConfirmation(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/v1/coworkers/settings" {
			t.Error("wrong gate request")
		}
		var body map[string]bool
		json.NewDecoder(r.Body).Decode(&body)
		if !body["enabled"] {
			t.Error("not enabled")
		}
		writes++
		json.NewEncoder(w).Encode(body)
	}))
	defer server.Close()
	a := New()
	a.Out, a.Err = &bytes.Buffer{}, &bytes.Buffer{}
	c := config.Context{Controller: server.URL}
	if err := a.controllerCoworkers(context.Background(), c, "test", []string{"enable"}); err == nil || writes != 0 {
		t.Fatal("unconfirmed gate changed")
	}
	if err := a.controllerCoworkers(context.Background(), c, "test", []string{"enable", "--confirm"}); err != nil || writes != 1 {
		t.Fatal("confirmed gate failed", err)
	}
}
