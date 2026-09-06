package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/config"
)

func (a *App) tokenPath(c config.Context) string {
	path := a.ConfigPath
	if path == "" {
		path = config.DefaultPath()
	}
	key := sha256.Sum256([]byte(c.Name + "\x00" + strings.TrimRight(c.Controller, "/")))
	return filepath.Join(filepath.Dir(path), "controller-tokens", fmt.Sprintf("%x", key))
}

func (a *App) saveControllerToken(c config.Context, token string) error {
	path := a.tokenPath(c)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("cannot create controller token directory")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".token-")
	if err != nil {
		return fmt.Errorf("cannot save controller token")
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(token)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return fmt.Errorf("cannot write controller token")
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("cannot save controller token")
	}
	return nil
}

func (a *App) controllerToken(ctx context.Context, c config.Context) (string, error) {
	if token := a.Environ[c.TokenEnv]; token != "" {
		return token, nil
	}
	data, err := os.ReadFile(a.tokenPath(c))
	if err == nil && len(data) > 0 {
		return string(data), nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("cannot read saved controller token")
	}
	if a.IsTerminal == nil || !a.IsTerminal() {
		return "", fmt.Errorf("controller authentication missing; run vmbox in a terminal to sign in, or set %s", c.TokenEnv)
	}
	token, err := a.readSecret("Controller token")
	if err != nil {
		return "", err
	}
	if _, err := a.requestOnce(ctx, c, token, http.MethodGet, "/v1/whoami", nil, nil, nil); err != nil {
		return "", fmt.Errorf("controller login failed; token was not saved: %w", err)
	}
	if err := a.saveControllerToken(c, token); err != nil {
		return "", err
	}
	return token, nil
}

func (a *App) controllerLogout(c config.Context) error {
	if err := os.Remove(a.tokenPath(c)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove saved controller token")
	}
	fmt.Fprintln(a.Out, "Saved controller token cleared for this context. Server token is not revoked.")
	if a.Environ[c.TokenEnv] != "" {
		fmt.Fprintf(a.Out, "An environment token is still set; run: unset %s\n", tuiLabel(c.TokenEnv, 100))
	}
	return nil
}
