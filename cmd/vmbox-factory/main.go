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
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(); err != nil {
		slog.Error("factory stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
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
	// Repository and execution adapters are deliberately not substituted with
	// fake repositories or canned agent output when configuration is absent.
	mux := http.NewServeMux()
	mux.Handle("/v1/factory/", service.Handler())
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
	slog.Info("factory backend listening", "address", address, "repositoryAccessConfigured", false)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
