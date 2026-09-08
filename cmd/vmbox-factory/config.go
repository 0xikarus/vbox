package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/githubapp"
	"github.com/0xikarus/vmbox-service/internal/secrets"
)

func githubEnvelope() (*secrets.Envelope, string, error) {
	app := os.Getenv("VMBOX_FACTORY_GITHUB_APP_ID")
	key := os.Getenv("VMBOX_FACTORY_ENCRYPTION_KEY")
	if app == "" || len(key) < 32 {
		return nil, "", fmt.Errorf("configure GitHub App ID and factory encryption key (at least 32 bytes)")
	}
	e, err := secrets.New([]byte(key))
	return e, "factory-github-app:" + app, err
}

// seal-github-key reads PEM from stdin and emits only an encrypted envelope.
// Configure its encryption key through secure environment, never arguments.
func sealGitHubKey() error {
	e, scope, err := githubEnvelope()
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	if err != nil || len(b) > 65536 {
		return fmt.Errorf("GitHub PEM input unavailable or oversized")
	}
	defer clear(b)
	if _, err = githubapp.New(githubapp.Config{AppID: os.Getenv("VMBOX_FACTORY_GITHUB_APP_ID"), PrivateKey: b}); err != nil {
		return fmt.Errorf("invalid GitHub App private key")
	}
	encrypted, err := e.Seal(scope, b)
	if err != nil {
		return fmt.Errorf("could not encrypt GitHub App key")
	}
	_, err = fmt.Fprintln(os.Stdout, encrypted)
	return err
}

func configureBackends(ctx context.Context, s *factory.Service) error {
	controllerURL := os.Getenv("VMBOX_FACTORY_CONTROLLER_URL")
	if controllerURL != "" {
		client := &factory.ControllerClient{URL: controllerURL, Token: os.Getenv("VMBOX_FACTORY_CONTROLLER_TOKEN"), AccountID: os.Getenv("VMBOX_FACTORY_ACCOUNT_ID")}
		if err := client.Authorize(ctx, client.AccountID); err != nil {
			return fmt.Errorf("factory controller account binding could not be verified")
		}
		s.Profiles = client.Profiles
	}
	path := os.Getenv("VMBOX_FACTORY_GITHUB_KEY_FILE")
	if path == "" {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 100000 {
		return fmt.Errorf("GitHub encrypted key file must be a regular private 0600 file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("GitHub encrypted key unavailable")
	}
	e, scope, err := githubEnvelope()
	if err != nil {
		return err
	}
	pem, err := e.Open(scope, strings.TrimSpace(string(b)))
	if err != nil {
		return fmt.Errorf("GitHub App key decryption failed")
	}
	defer clear(pem)
	var installations map[string][]int64
	if err = json.Unmarshal([]byte(os.Getenv("VMBOX_FACTORY_GITHUB_INSTALLATIONS")), &installations); err != nil || len(installations) == 0 {
		return fmt.Errorf("configure trusted account-to-installation allowlist JSON")
	}
	client, err := githubapp.New(githubapp.Config{AppID: os.Getenv("VMBOX_FACTORY_GITHUB_APP_ID"), PrivateKey: pem, Installations: installations})
	if err != nil {
		return fmt.Errorf("invalid GitHub App configuration")
	}
	s.Repositories = client
	return nil
}
