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
	// VMBOX_SHARED_SLOTS only seeds the first start; the controller changes
	// slots afterwards and the persisted value wins.
	capacity := 0
	if raw := os.Getenv("VMBOX_SHARED_SLOTS"); raw != "" {
		var err error
		if capacity, err = strconv.Atoi(raw); err != nil || capacity < 1 || capacity > 32 {
			return errors.New("VMBOX_SHARED_SLOTS must be an integer from 1 to 32 when set")
		}
	}
	root := os.Getenv("VMBOX_SHARED_ROOT")
	if root == "" {
		root = "/data"
	}
	mode, err := sharedworker.ParseIsolationMode(os.Getenv("VMBOX_SHARED_ISOLATION"))
	if err != nil {
		return err
	}
	runtime := &sharedworker.LinuxRuntime{Root: root, Binary: "/usr/local/bin/vmbox-runtime"}
	var status sharedworker.IsolationStatus
	if mode == sharedworker.IsolationModeContainer {
		container, err := sharedworker.NewContainerRuntime(root, os.Getenv("VMBOX_SHARED_CONTAINER_IMAGE"))
		if err != nil {
			return err
		}
		runtime.Container = container
		runtime.Isolation = sharedworker.IsolationStatus{Mode: mode, Tier: sharedworker.IsolationTierContainer}
		status = runtime.Isolation
	} else {
		status = runtime.ConfigureIsolation(context.Background(), mode)
	}
	log.Printf("shared worker isolation: tier=%s mode=%s reason=%s", status.Tier, status.Mode, status.Reason)
	store, err := sharedworker.Open(root, os.Getenv("VMBOX_SHARED_ACCOUNT_ID"), capacity, runtime)
	if err != nil {
		return err
	}
	defer store.Close()
	if saved := store.SlotCapacity(); capacity != 0 && capacity != saved {
		log.Printf("shared worker: VMBOX_SHARED_SLOTS=%d ignored; using %d slots saved through the controller", capacity, saved)
	}
	handler, err := sharedworker.NewServer(store, runtime, os.Getenv("VMBOX_SHARED_TOKEN"))
	if err != nil {
		return err
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	server := &http.Server{Addr: os.Getenv("VMBOX_SHARED_BIND") + ":" + port, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
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
