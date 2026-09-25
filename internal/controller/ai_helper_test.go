package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestOpenRouterRewriteUsesSavedKeyAndSelectedModel(t *testing.T) {
	profile := v1.SaveLoginProfileRequest{Files: map[string][]byte{"auth.json": []byte(`{"openrouter":{"type":"api","key":"synthetic-private-key"}}`)}}
	key, model, err := openRouterProfileKey(profile, "openrouter/google/test-model")
	if err != nil || key != "synthetic-private-key" || model != "google/test-model" {
		t.Fatalf("unexpected profile lookup: model=%q error=%v", model, err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer synthetic-private-key" {
			t.Errorf("request did not use the saved key")
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Model != "google/test-model" || len(body.Messages) != 2 || body.Messages[0].Role != "system" || !strings.Contains(body.Messages[0].Content, "Keep the intent") || body.Messages[1].Content != "Helo there" {
			t.Errorf("unexpected rewrite request: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"Hello there"}}]}`))
	}))
	defer upstream.Close()
	got, err := requestOpenRouterRewrite(context.Background(), upstream.Client(), upstream.URL, key, model, rewriteRequest{Kind: "chat", Text: "Helo there", Instruction: "Keep the intent"})
	if err != nil || got != "Hello there" {
		t.Fatalf("rewrite = %q, %v", got, err)
	}
}

func TestOpenRouterRewriteRejectsTruncationAndHidesUpstreamDetails(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"truncated", `{"choices":[{"finish_reason":"length","message":{"content":"partial"}}]}`, 200},
		{"rejected", `{"error":{"message":"synthetic-private-key"}}`, 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			_, err := requestOpenRouterRewrite(context.Background(), upstream.Client(), upstream.URL, "synthetic-private-key", "test/model", rewriteRequest{Kind: "markdown", Text: "# Helo"})
			if err == nil || strings.Contains(err.Error(), "synthetic-private-key") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
		})
	}
}
