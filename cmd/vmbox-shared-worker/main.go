package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/0xikarus/vmbox-service/internal/sharedworker"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if os.Geteuid() != 0 {
		return errors.New("shared worker supervisor must run as root; workloads use separate unprivileged users")
	}
	capacity, err := strconv.Atoi(os.Getenv("VMBOX_SHARED_SLOTS"))
	if err != nil {
		return errors.New("VMBOX_SHARED_SLOTS must be an integer from 1 to 32")
	}
	root := os.Getenv("VMBOX_SHARED_ROOT")
	if root == "" {
		root = "/data"
	}
	runtime := &sharedworker.LinuxRuntime{Root: root, Binary: "/usr/local/bin/vmbox-runtime"}
	store, err := sharedworker.Open(root, os.Getenv("VMBOX_SHARED_ACCOUNT_ID"), capacity, runtime)
	if err != nil {
		return err
	}
	defer store.Close()
	handler, err := sharedworker.NewServer(store, runtime, os.Getenv("VMBOX_SHARED_TOKEN"))
	if err != nil {
		return err
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	server := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, finish := context.WithTimeout(context.Background(), 10*time.Second)
		defer finish()
		return server.Shutdown(shutdown)
	}
}
