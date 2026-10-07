package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestCaptchaProviderFrameAllowlist pins the strict origin matching: a frame
// URL that merely CONTAINS provider text must never be treated as a widget.
func TestCaptchaProviderFrameAllowlist(t *testing.T) {
	cases := []struct {
		kind string
		url  string
		want string
	}{
		{"recaptcha", "https://www.google.com/recaptcha/api2/anchor?k=k", "widget"},
		{"recaptcha", "https://www.google.com/recaptcha/enterprise/anchor", "widget"},
		{"recaptcha", "https://recaptcha.google.com/recaptcha/api2/anchor", "widget"},
		{"recaptcha", "https://www.gstatic.com/recaptcha/api2/anchor", "widget"},
		{"recaptcha", "https://evil.test/?x=google.com/recaptcha", "page"},
		{"recaptcha", "https://google.com.evil.test/recaptcha", "page"},
		{"recaptcha", "https://evil.test/google.com/recaptcha/api2", "page"},
		{"hcaptcha", "https://js.hcaptcha.com/1/api.js", "widget"},
		{"hcaptcha", "https://api.hcaptcha.com/getcaptcha", "widget"},
		{"hcaptcha", "https://evil.test/?x=hcaptcha.com", "page"},
		{"hcaptcha", "https://hcaptcha.com.evil.test/x", "page"},
		{"turnstile", "https://challenges.cloudflare.com/turnstile/v0/x.html", "widget"},
		{"turnstile", "https://evil.test/?x=challenges.cloudflare.com", "page"},
	}
	for _, tc := range cases {
		if got := captchaProviderFrame(tc.kind, tc.url); got != tc.want {
			t.Errorf("captchaProviderFrame(%q, %q)=%q want %q", tc.kind, tc.url, got, tc.want)
		}
	}
}

// TestSameCaptchaPage pins the injection binding: token delivery must not jump
// to an unrelated tab that happens to host the same challenge type.
func TestSameCaptchaPage(t *testing.T) {
	cases := []struct {
		card   string
		target string
		want   bool
	}{
		{"https://site.test/login", "https://site.test/login", true},
		{"https://site.test/login?x=1", "https://site.test/login", false},
		{"https://site.test/login?x=1", "https://site.test/login?x=2", false},
		{"https://site.test/login", "https://site.test/signup", false},
		{"https://site.test/login", "https://other.test/login", false},
		{"https://site.test/login", "http://site.test/login", false},
		{"", "https://site.test/login", true},
	}
	for _, tc := range cases {
		if got := sameCaptchaPage(tc.card, tc.target); got != tc.want {
			t.Errorf("sameCaptchaPage(%q, %q)=%t want %t", tc.card, tc.target, got, tc.want)
		}
	}
}

// TestSubmitCaptchaAnswerRejectsAttackerFrames drives a fake CDP browser: an
// iframe whose URL merely contains "google.com/recaptcha" must never receive
// the token, and the answer only lands on the page the card came from. The
// fake browser tracks which target every Runtime.evaluate ran on, so this
// fails if a matcher-based implementation ever attaches to the attacker frame.
func TestSubmitCaptchaAnswerRejectsAttackerFrames(t *testing.T) {
	type evaluation struct {
		targetID   string
		expression string
	}
	var mu sync.Mutex
	var evaluations []evaluation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		sessions := map[string]string{} // sessionId -> targetId
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var request struct {
				ID      int64           `json:"id"`
				Session string          `json:"sessionId"`
				Method  string          `json:"method"`
				Params  json.RawMessage `json:"params"`
			}
			if json.Unmarshal(data, &request) != nil {
				continue
			}
			var params struct {
				URL        string `json:"url"`
				TargetID   string `json:"targetId"`
				Expression string `json:"expression"`
			}
			_ = json.Unmarshal(request.Params, &params)
			switch request.Method {
			case "Target.getTargets":
				payload, _ := json.Marshal(map[string]any{"id": request.ID, "result": map[string]any{"targetInfos": []map[string]any{
					{"targetId": "page-site", "type": "page", "url": "https://site.test/login"},
					{"targetId": "iframe-attacker", "type": "iframe", "url": "https://evil.test/?x=google.com/recaptcha"},
				}}})
				_ = conn.Write(ctx, websocket.MessageText, payload)
			case "Target.attachToTarget":
				sessions["session-1"] = params.TargetID
				payload, _ := json.Marshal(map[string]any{"id": request.ID, "result": map[string]any{"sessionId": "session-1"}})
				_ = conn.Write(ctx, websocket.MessageText, payload)
			case "Target.detachFromTarget":
				payload, _ := json.Marshal(map[string]any{"id": request.ID, "result": map[string]any{}})
				_ = conn.Write(ctx, websocket.MessageText, payload)
			case "Runtime.evaluate":
				mu.Lock()
				evaluations = append(evaluations, evaluation{targetID: sessions[request.Session], expression: params.Expression})
				mu.Unlock()
				payload, _ := json.Marshal(map[string]any{"id": request.ID, "result": map[string]any{"result": map[string]any{"value": "no-target"}}})
				_ = conn.Write(ctx, websocket.MessageText, payload)
			default:
				payload, _ := json.Marshal(map[string]any{"id": request.ID, "result": map[string]any{}})
				_ = conn.Write(ctx, websocket.MessageText, payload)
			}
		}
	}))
	defer server.Close()

	profile := t.TempDir()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
	if err := os.WriteFile(profile+"/DevToolsActivePort", []byte(port+"\n/devtools/browser/test"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := Connect(ctx, profile)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	answer := CaptchaAnswer{Type: "recaptcha", Token: "TOKEN-VALUE-123456", PageURL: "https://site.test/login"}
	if err := client.SubmitCaptchaAnswer(ctx, answer); err == nil {
		t.Fatal("submit should report no open challenge here")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, evaluated := range evaluations {
		if !strings.Contains(evaluated.expression, "TOKEN-VALUE-123456") {
			continue
		}
		// A token write on any target other than the card's page is a leak —
		// this run's fake browser exposes exactly the attacker iframe, so any
		// token evaluation here fails.
		if evaluated.targetID != "page-site" {
			t.Fatalf("token evaluated on target %q (%s)", evaluated.targetID, evaluated.expression)
		}
	}
}
