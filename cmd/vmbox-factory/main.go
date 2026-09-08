// vmbox-factory is a separately enabled backend for the controller's Factory tab.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/assets"
	"github.com/0xikarus/vmbox-service/internal/factory/resultinbox"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(); err != nil {
		slog.Error("factory stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) == 2 && os.Args[1] == "seal-github-key" {
		return sealGitHubKey()
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	dsn := os.Getenv("VMBOX_FACTORY_DATABASE_URL")
	root := os.Getenv("VMBOX_FACTORY_DATA_DIR")
	token := os.Getenv("VMBOX_FACTORY_GATEWAY_TOKEN")
	if dsn == "" || root == "" || len(token) < 32 {
		return fmt.Errorf("set factory database URL, persistent data directory, and a gateway token of at least 32 characters")
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
		return fmt.Errorf("factory data directory must be a dedicated absolute path")
	}
	limit := 3
	if value := os.Getenv("VMBOX_FACTORY_MAX_WORKERS"); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 6 {
			return fmt.Errorf("factory worker limit must be between 1 and 6")
		}
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("invalid factory database configuration")
	}
	defer db.Close()
	setup, cancelSetup := context.WithTimeout(ctx, 30*time.Second)
	defer cancelSetup()
	if err = db.PingContext(setup); err != nil {
		return fmt.Errorf("factory database unavailable")
	}
	store := &factory.Store{DB: db}
	if err = store.Migrate(setup); err != nil {
		return fmt.Errorf("factory database migration failed")
	}
	assetStore, err := assets.New(filepath.Join(root, "assets"))
	if err != nil {
		return fmt.Errorf("private factory asset directory unavailable")
	}
	defer assetStore.Close()
	service := &factory.Service{Store: store, GatewayToken: token, Assets: factory.PrivateAssets{Store: assetStore}, WorkerLimit: limit}
	if err = configureBackends(setup, service); err != nil {
		return err
	}
	// Repository and execution adapters are deliberately not substituted with
	// fake repositories or canned agent output when configuration is absent.
	mux := http.NewServeMux()
	mux.Handle("/v1/factory/", service.Handler())
	// This route accepts only an attempt-scoped callback capability, not the
	// gateway/controller token. Mount on the factory service's own TLS endpoint.
	var inbox *resultinbox.Inbox
	if secret := os.Getenv("VMBOX_FACTORY_RESULT_SECRET"); secret != "" {
		inbox, err = resultinbox.New(db, []byte(secret), 0)
		if err != nil {
			return fmt.Errorf("invalid factory result secret")
		}
		if err = inbox.Migrate(setup); err != nil {
			return fmt.Errorf("result inbox migration failed")
		}
		mux.Handle("/result", inbox.Handler())
	}
	dispatcher, err := configurePlanning(setup, service, inbox)
	if err != nil {
		return err
	}
	publication, err := configurePublication(setup, service)
	if err != nil {
		return err
	}
	if publication != nil {
		done := make(chan struct{})
		go func() { defer close(done); runPublication(ctx, publication) }()
		defer func() { cancel(); <-done }()
	}
	if dispatcher != nil {
		done := make(chan struct{})
		go func() {
			defer close(done)
			dispatcher.Run(ctx, func(error) { slog.Warn("planning operation deferred; durable work item retains its status") })
		}()
		defer func() { cancel(); <-done }()
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		c, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if db.PingContext(c) != nil {
			http.Error(w, "database unavailable", 503)
			return
		}
		w.WriteHeader(204)
	})
	address := os.Getenv("VMBOX_FACTORY_LISTEN")
	if strings.TrimSpace(address) == "" {
		address = "127.0.0.1:8090"
	}
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("factory backend listening", "address", address, "repositoryAccessConfigured", service.Repositories != nil)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
