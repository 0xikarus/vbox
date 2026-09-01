package main

import (
	"context"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/hostd"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	incusprovider "github.com/0xikarus/vmbox-service/internal/provider/incus"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	token := os.Getenv("VMBOX_HOSTD_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "vmbox-hostd: VMBOX_HOSTD_TOKEN is required")
		os.Exit(1)
	}
	provider := incusprovider.New(incusprovider.Config{Remote: os.Getenv("VMBOX_INCUS_REMOTE"), Project: os.Getenv("VMBOX_INCUS_PROJECT"), DefaultImage: os.Getenv("VMBOX_INCUS_IMAGE"), VM: os.Getenv("VMBOX_INCUS_VM") == "1"}, procexec.OSRunner{})
	controller := os.Getenv("VMBOX_CONTROLLER_URL")
	if controller != "" {
		go func() {
			_ = hostd.Heartbeat(ctx, nil, controller, os.Getenv("VMBOX_CONTROLLER_TOKEN"), value("VMBOX_HOST_ID", "ubuntu-host"), provider)
		}()
	}
	server := &http.Server{Addr: value("VMBOX_HOSTD_LISTEN", "127.0.0.1:8081"), Handler: (&hostd.Server{Provider: provider, Token: token}).Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "vmbox-hostd:", err)
		os.Exit(1)
	}
}
func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
