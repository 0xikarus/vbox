package cli

import (
	"bytes"
	"context"
	"encoding/json"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWhoami(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" || r.URL.Path != "/v1/whoami" {
				t.Error("identity lookup must be read-only and provider-independent")
			}
			json.NewEncoder(w).Encode(v1.Identity{AccountID: "account-id", AccountName: "team", UserID: "user-id", Subject: "alice", Role: "user"})
		}))
		a := New()
		var out bytes.Buffer
		a.Out = &out
		a.Environ = map[string]string{"AUTH": "synthetic-private-token"}
		args := []string{"whoami"}
		if asJSON {
			args = append(args, "--json")
		}
		err := a.controller(context.Background(), config.File{}, config.Context{Controller: s.URL, TokenEnv: "AUTH"}, args)
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "alice") || strings.Contains(out.String(), "synthetic-private-token") {
			t.Fatal("identity output invalid")
		}
		if asJSON {
			var identity v1.Identity
			if json.Unmarshal(out.Bytes(), &identity) != nil || identity.AccountID != "account-id" {
				t.Fatal("invalid JSON")
			}
		}
	}
}
