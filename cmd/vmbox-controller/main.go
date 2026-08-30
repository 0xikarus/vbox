package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/0xikarus/vmbox-service/internal/controller"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	dockerprovider "github.com/0xikarus/vmbox-service/internal/provider/docker"
	incusprovider "github.com/0xikarus/vmbox-service/internal/provider/incus"
	railwayprovider "github.com/0xikarus/vmbox-service/internal/provider/railway"
	sevallaprovider "github.com/0xikarus/vmbox-service/internal/provider/sevalla"
)

func main() {
	if err := run(); err != nil {
		slog.Error("controller stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	store, err := controller.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return err
	}
	if len(os.Args) > 1 && os.Args[1] == "bootstrap" {
		token, err := randomToken()
		if err != nil {
			return err
		}
		account := env("VMBOX_ACCOUNT_NAME", "default")
		subject := env("VMBOX_OWNER_SUBJECT", "owner")
		p, err := store.Bootstrap(ctx, account, subject, token)
		if err != nil {
			return err
		}
		fmt.Printf("account=%s user=%s\nVMBOX_CONTROLLER_TOKEN=%s\n", p.AccountID, p.UserID, token)
		return nil
	}
	registry := providers()
	server := controller.NewServer(store, registry)
	server.PublicURL = os.Getenv("VMBOX_CONTROLLER_URL")
	server.DefaultImage = os.Getenv("VMBOX_IMAGE")
	httpServer := &http.Server{Addr: env("VMBOX_CONTROLLER_LISTEN", ":8080"), Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		_ = httpServer.Shutdown(shutdown)
	}()
	slog.Info("controller listening", "address", httpServer.Addr, "providers", registry.Names())
	err = httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
func providers() *provider.Registry {
	runner := procexec.OSRunner{}
	return provider.NewRegistry(dockerprovider.New(dockerprovider.Config{Context: os.Getenv("DOCKER_CONTEXT"), Host: os.Getenv("DOCKER_HOST"), TLSVerify: os.Getenv("DOCKER_TLS_VERIFY") != "", CertPath: os.Getenv("DOCKER_CERT_PATH"), DefaultImage: os.Getenv("VMBOX_IMAGE")}, runner), railwayprovider.New(railwayprovider.Config{ProjectID: os.Getenv("VMBOX_RAILWAY_PROJECT_ID"), EnvironmentID: os.Getenv("VMBOX_RAILWAY_ENVIRONMENT_ID"), Token: os.Getenv("RAILWAY_API_TOKEN"), DefaultImage: os.Getenv("VMBOX_IMAGE")}, runner), sevallaprovider.New(sevallaprovider.Config{Token: os.Getenv("SEVALLA_API_TOKEN"), APIURL: os.Getenv("SEVALLA_API_URL"), CompanyID: os.Getenv("VMBOX_SEVALLA_COMPANY_ID"), ProjectID: os.Getenv("VMBOX_SEVALLA_PROJECT_ID"), ClusterID: os.Getenv("VMBOX_SEVALLA_CLUSTER_ID"), ResourceTypeID: os.Getenv("VMBOX_SEVALLA_RESOURCE_TYPE_ID"), DefaultImage: os.Getenv("VMBOX_IMAGE"), PreAttachedDisk: os.Getenv("VMBOX_SEVALLA_DISK_ID")}), incusprovider.New(incusprovider.Config{Remote: os.Getenv("VMBOX_INCUS_REMOTE"), Project: os.Getenv("VMBOX_INCUS_PROJECT"), DefaultImage: os.Getenv("VMBOX_INCUS_IMAGE"), VM: os.Getenv("VMBOX_INCUS_VM") == "1"}, runner))
}
func randomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
