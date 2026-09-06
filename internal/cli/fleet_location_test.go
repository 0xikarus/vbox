package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/0xikarus/vmbox-service/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFleetLocationSet(t *testing.T) {
	for _, status := range []int{200, 409} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "PUT" || r.URL.Path != "/v1/fleet/location" {
				t.Error("wrong route")
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["region"] != "new-region" || body["provider"] != "test" || body["providerCredential"] != "primary" {
				t.Error("incorrect target")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]string{"error": "fleet must be empty"})
		}))
		var out bytes.Buffer
		a := &App{Out: &out, HTTP: server.Client()}
		err := a.controllerFleetLocation(context.Background(), config.Context{Controller: server.URL, Provider: "test", ProviderCredential: "primary"}, "test", []string{"set", "new-region"})
		server.Close()
		if status == 200 && (err != nil || !strings.Contains(out.String(), "new-region")) {
			t.Fatalf("%v %s", err, out.String())
		}
		if status == 409 && (err == nil || out.Len() != 0) {
			t.Fatal("conflict reported as success")
		}
	}
}
