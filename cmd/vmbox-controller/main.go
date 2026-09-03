package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/0xikarus/vmbox-service/internal/controller"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	dockerprovider "github.com/0xikarus/vmbox-service/internal/provider/docker"
	incusprovider "github.com/0xikarus/vmbox-service/internal/provider/incus"
	railwayprovider "github.com/0xikarus/vmbox-service/internal/provider/railway"
	"github.com/0xikarus/vmbox-service/internal/secrets"
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
	store, err := openStore(ctx, os.Getenv("DATABASE_URL"))
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
	if encoded := os.Getenv("VMBOX_BOOTSTRAP_TOKEN_HASH"); encoded != "" {
		hasAccounts, err := store.HasAccounts(ctx)
		if err != nil {
			return err
		}
		if !hasAccounts {
			hash, err := base64.RawURLEncoding.DecodeString(encoded)
			if err != nil {
				return fmt.Errorf("VMBOX_BOOTSTRAP_TOKEN_HASH: %w", err)
			}
			if _, err := store.BootstrapHash(ctx, env("VMBOX_ACCOUNT_NAME", "default"), env("VMBOX_OWNER_SUBJECT", "owner"), hash); err != nil {
				return err
			}
		}
	}
	key, err := encryptionKey(os.Getenv("VMBOX_ENCRYPTION_KEY"))
	if err != nil {
		return err
	}
	store.Envelope, err = secrets.New(key)
	if err != nil {
		return err
	}
	registry := provider.NewRegistry()
	server := controller.NewServer(store, registry)
	server.PublicURL = os.Getenv("VMBOX_CONTROLLER_URL")
	server.DefaultImage = os.Getenv("VMBOX_IMAGE")
	server.Resolve = func(resolveCtx context.Context, accountID, providerName, credentialName string) (provider.Provider, error) {
		credential, err := store.ProviderCredential(resolveCtx, accountID, providerName, credentialName)
		if err != nil {
			return nil, err
		}
		return providerForCredential(providerName, credential)
	}
	server.Bootstrap = bootstrapWorkload
	if err := server.StartReconciler(ctx); err != nil {
		return fmt.Errorf("startup reconciliation: %w", err)
	}
	listen := os.Getenv("VMBOX_CONTROLLER_LISTEN")
	if listen == "" {
		listen = ":8080"
		if port := os.Getenv("PORT"); port != "" {
			listen = ":" + port
		}
	}
	httpServer := &http.Server{Addr: listen, Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute}
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
func openStore(ctx context.Context, dsn string) (*controller.Store, error) {
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	for attempt := 1; ; attempt++ {
		store, err := controller.Open(ctx, dsn)
		if err == nil {
			return store, nil
		}
		if attempt == 1 || attempt%5 == 0 {
			slog.Warn("waiting for controller database", "attempt", attempt, "error", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("controller database did not become ready: %w", err)
		case <-time.After(2 * time.Second):
		}
	}
}
func bootstrapWorkload(ctx context.Context, p provider.Provider, box provider.Box, components []string) error {
	bootstrapper, ok := p.(provider.Bootstrapper)
	if !ok {
		return nil
	}
	runtimeBinary, err := os.ReadFile("/usr/local/bin/vmbox-runtime")
	if err != nil {
		return fmt.Errorf("read controller runtime asset: %w", err)
	}
	entrypoint, err := os.ReadFile("/usr/local/bin/vmbox-entrypoint")
	if err != nil {
		return fmt.Errorf("read controller entrypoint asset: %w", err)
	}
	request := provider.BootstrapRequest{
		Components:      append([]string(nil), components...),
		RuntimeBinaries: map[string][]byte{runtime.GOARCH: runtimeBinary},
		Entrypoint:      entrypoint,
	}
	return bootstrapper.Bootstrap(ctx, box.ID, request)
}

func providerForCredential(name string, credential controller.DecryptedProviderCredential) (provider.Provider, error) {
	var secret, config map[string]any
	if err := json.Unmarshal(credential.Secret, &secret); err != nil {
		return nil, fmt.Errorf("decode %s credential secret: %w", name, err)
	}
	if len(credential.Config) > 0 {
		if err := json.Unmarshal(credential.Config, &config); err != nil {
			return nil, fmt.Errorf("decode %s credential config: %w", name, err)
		}
	}
	stringValue := func(values map[string]any, key string) string {
		value, _ := values[key].(string)
		return value
	}
	boolValue := func(values map[string]any, key string) bool {
		value, _ := values[key].(bool)
		return value
	}
	switch name {
	case "railway":
		token := stringValue(secret, "token")
		if token == "" {
			return nil, fmt.Errorf("Railway credential token is required")
		}
		tokenEnvironment := stringValue(config, "tokenEnvironment")
		if tokenEnvironment == "" {
			tokenEnvironment = "RAILWAY_API_TOKEN"
		}
		if tokenEnvironment != "RAILWAY_API_TOKEN" && tokenEnvironment != "RAILWAY_TOKEN" {
			return nil, fmt.Errorf("Railway tokenEnvironment must be RAILWAY_API_TOKEN or RAILWAY_TOKEN")
		}
		runner, knownHosts, err := controllerRailwayRunner(token, tokenEnvironment)
		if err != nil {
			return nil, err
		}
		return railwayprovider.New(railwayprovider.Config{ProjectID: stringValue(config, "projectId"), EnvironmentID: stringValue(config, "environmentId"), Token: token, TokenEnvironment: tokenEnvironment, DefaultImage: stringValue(config, "image"), SSHKnownHostsFile: knownHosts, SSHBinary: runner.Env["VMBOX_REAL_SSH"], SSHControlDir: runner.Env["VMBOX_RAILWAY_CONTROL_DIR"]}, runner), nil
	case "docker":
		return dockerprovider.New(dockerprovider.Config{Context: stringValue(config, "context"), Host: stringValue(config, "host"), TLSVerify: boolValue(config, "tlsVerify"), CertPath: stringValue(config, "certPath"), DefaultImage: stringValue(config, "image")}, procexec.OSRunner{}), nil
	case "incus":
		return incusprovider.New(incusprovider.Config{Remote: stringValue(config, "remote"), Project: stringValue(config, "project"), DefaultImage: stringValue(config, "image"), VM: boolValue(config, "vm")}, procexec.OSRunner{}), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", name)
	}
}

func controllerRailwayRunner(token, tokenEnvironment string) (procexec.OSRunner, string, error) {
	realSSH, err := exec.LookPath("ssh")
	if err != nil {
		return procexec.OSRunner{}, "", fmt.Errorf("locate OpenSSH client: %w", err)
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "/data/home"
	}
	sshDir := filepath.Join(home, ".local", "share", "vmbox", "railway-ssh")
	controlDir := filepath.Join(sshDir, "control")
	knownHosts := filepath.Join(home, ".config", "vmbox", "railway-known-hosts")
	if err := os.MkdirAll(controlDir, 0700); err != nil {
		return procexec.OSRunner{}, "", err
	}
	if err := os.MkdirAll(filepath.Dir(knownHosts), 0700); err != nil {
		return procexec.OSRunner{}, "", err
	}
	other := "RAILWAY_API_TOKEN"
	if tokenEnvironment == other {
		other = "RAILWAY_TOKEN"
	}
	path := os.Getenv("PATH")
	env := map[string]string{tokenEnvironment: token, "PATH": path, "VMBOX_REAL_SSH": realSSH, "VMBOX_RAILWAY_KNOWN_HOSTS": knownHosts, "VMBOX_RAILWAY_CONTROL_DIR": controlDir}
	return procexec.OSRunner{Env: env, Unset: []string{other}}, knownHosts, nil
}

func encryptionKey(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("VMBOX_ENCRYPTION_KEY is required")
	}
	key, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		key, err = base64.StdEncoding.DecodeString(value)
	}
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("VMBOX_ENCRYPTION_KEY must be base64-encoded 32 bytes")
	}
	return key, nil
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
