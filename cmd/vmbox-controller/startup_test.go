package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/controller"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestSlowStartupReconciliationDoesNotBlockHTTP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	returned := make(chan (<-chan struct{}), 1)
	go func() {
		returned <- startReconciliation(ctx, func(ctx context.Context) error {
			close(entered)
			<-ctx.Done() // A provider call that cannot finish until cancelled.
			return ctx.Err()
		})
	}()
	defer cancel()
	var done <-chan struct{}
	select {
	case done = <-returned:
	case <-time.After(time.Second):
		t.Fatal("provider recovery blocked HTTP startup")
	}
	<-entered
	s := controller.NewServer(nil, provider.NewRegistry())
	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()
	client := &http.Client{Timeout: time.Second}
	for path, want := range map[string]int{"/healthz": http.StatusOK, "/v1/whoami": http.StatusUnauthorized} {
		resp, err := client.Get(httpServer.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s: status %d, want %d", path, resp.StatusCode, want)
		}
	}
	select {
	case <-done:
		t.Fatal("reconciliation stopped before cancellation")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("startup reconciliation did not stop on shutdown")
	}
}
