package coworker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChannelHandshakeAndToolProxy(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-box-token" {
			t.Error("per-box token missing")
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		calls++
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"tools": []any{}}})
	}))
	defer server.Close()
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}\n")
	var out bytes.Buffer
	err := (Client{URL: server.URL, Token: "synthetic-box-token"}).ClaudeChannel(context.Background(), input, &out)
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	if !strings.Contains(out.String(), "claude/channel") || strings.Contains(out.String(), "claude/channel/permission") || strings.Contains(out.String(), "synthetic-box-token") {
		t.Fatal("incorrect channel capabilities or token disclosure")
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer origin.Close()
	_, err := (Client{URL: origin.URL, Token: "synthetic"}).Events(context.Background(), 0)
	if err == nil || reached {
		t.Fatal("credential-bearing redirect followed")
	}
}
