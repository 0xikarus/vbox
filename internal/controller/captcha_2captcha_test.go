package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestTwoCaptchaSolveWidgetsAndImages(t *testing.T) {
	var polls atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("POST /in.php", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.PostForm.Get("method") {
		case "userrecaptcha", "hcaptcha", "turnstile", "base64":
		default:
			t.Fatalf("unexpected method %q", r.PostForm.Get("method"))
		}
		if r.PostForm.Get("key") != "testkey12345" {
			t.Fatalf("unexpected key %q", r.PostForm.Get("key"))
		}
		_, _ = w.Write([]byte(`{"status":1,"request":"task-1"}`))
	})
	mux.HandleFunc("POST /res.php", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("id") != "task-1" || r.PostForm.Get("action") != "get" {
			t.Fatalf("unexpected poll %v", r.PostForm)
		}
		if polls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"status":0,"request":"CAPCHA_NOT_READY"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":1,"request":"TOKEN-VALUE"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	solver := twoCaptchaSolver{Client: server.Client(), BaseURL: server.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	previous := twoCaptchaPollInterval
	twoCaptchaPollInterval = 10 * time.Millisecond
	defer func() { twoCaptchaPollInterval = previous }()

	for _, kind := range []string{"recaptcha", "hcaptcha", "turnstile"} {
		answer, err := solver.Solve(ctx, "testkey12345", v1.BoxMessageCaptcha{Type: kind, URL: "https://example.test/login", SiteKey: "site-key"}, nil)
		if err != nil || answer != "TOKEN-VALUE" {
			t.Fatalf("%s solve=(%q,%v)", kind, answer, err)
		}
	}
	answer, err := solver.Solve(ctx, "testkey12345", v1.BoxMessageCaptcha{Type: "image"}, []byte("png-bytes"))
	if err != nil || answer != "TOKEN-VALUE" {
		t.Fatalf("image solve=(%q,%v)", answer, err)
	}
	if _, err := solver.Solve(ctx, "testkey12345", v1.BoxMessageCaptcha{Type: "recaptcha"}, nil); err == nil {
		t.Fatal("missing site key accepted")
	}
}

func TestTwoCaptchaRejectsAndFailsSurfaceErrors(t *testing.T) {
	var submitted, polled atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("POST /in.php", func(w http.ResponseWriter, r *http.Request) {
		if submitted.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"status":0,"request":"ERROR_BAD_KEY"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":1,"request":"task-2"}`))
	})
	mux.HandleFunc("POST /res.php", func(w http.ResponseWriter, r *http.Request) {
		if polled.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"status":0,"request":"ERROR_CAPTCHA_UNSOLVABLE"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":0,"request":"CAPCHA_NOT_READY"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	previous := twoCaptchaPollInterval
	twoCaptchaPollInterval = 10 * time.Millisecond
	defer func() { twoCaptchaPollInterval = previous }()

	solver := twoCaptchaSolver{Client: server.Client(), BaseURL: server.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := solver.Solve(ctx, "bad", v1.BoxMessageCaptcha{Type: "recaptcha", URL: "https://example.test", SiteKey: "k"}, nil); err == nil {
		t.Fatal("submission error accepted silently")
	} else if !strings.Contains(strings.ToLower(err.Error()), "bad key") {
		t.Fatalf("submission error lost detail: %v", err)
	}
	if _, err := solver.Solve(ctx, "bad", v1.BoxMessageCaptcha{Type: "recaptcha", URL: "https://example.test", SiteKey: "k"}, nil); err == nil {
		t.Fatal("solver failure accepted silently")
	} else if !strings.Contains(strings.ToLower(err.Error()), "captcha unsolvable") {
		t.Fatalf("solver failure lost detail: %v", err)
	}
}

func TestTwoCaptchaTimesOutWhenNeverReady(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /in.php", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":1,"request":"task-3"}`))
	})
	mux.HandleFunc("POST /res.php", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":0,"request":"CAPCHA_NOT_READY"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	previousPoll, previousWait := twoCaptchaPollInterval, twoCaptchaMaxWait
	twoCaptchaPollInterval = 5 * time.Millisecond
	twoCaptchaMaxWait = 50 * time.Millisecond
	defer func() { twoCaptchaPollInterval, twoCaptchaMaxWait = previousPoll, previousWait }()

	solver := twoCaptchaSolver{Client: server.Client(), BaseURL: server.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := solver.Solve(ctx, "testkey12345", v1.BoxMessageCaptcha{Type: "hcaptcha", URL: "https://example.test", SiteKey: "k"}, nil); err == nil {
		t.Fatal("missing answer deadline not enforced")
	}
}
