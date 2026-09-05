//go:build vmbox_operator

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/0xikarus/vmbox-service/internal/procexec"
)

func railwayToken(environ map[string]string) (string, string, error) {
	project, account := environ["RAILWAY_TOKEN"], environ["RAILWAY_API_TOKEN"]
	if project != "" && account != "" {
		return "", "", fmt.Errorf("set only one of RAILWAY_TOKEN (project) or RAILWAY_API_TOKEN (account)")
	}
	if project != "" {
		return project, "RAILWAY_TOKEN", nil
	}
	if account != "" {
		return account, "RAILWAY_API_TOKEN", nil
	}
	return "", "", fmt.Errorf("RAILWAY_TOKEN or RAILWAY_API_TOKEN is required")
}

func railwayRunner(token, environment string, environ map[string]string) (procexec.OSRunner, string, error) {
	other := "RAILWAY_API_TOKEN"
	if environment == other {
		other = "RAILWAY_TOKEN"
	}
	env := make(map[string]string)
	unset := []string(nil)
	if token != "" {
		env[environment] = token
		unset = append(unset, other)
	}
	realSSH, err := exec.LookPath("ssh")
	if err != nil {
		return procexec.OSRunner{}, "", fmt.Errorf("locate OpenSSH client: %w", err)
	}
	dataHome := environ["XDG_DATA_HOME"]
	configHome := environ["XDG_CONFIG_HOME"]
	home := environ["HOME"]
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	sshDir := filepath.Join(dataHome, "vmbox", "railway-ssh")
	controlDir := filepath.Join(sshDir, "control")
	knownHosts := filepath.Join(configHome, "vmbox", "railway-known-hosts")
	if err := os.MkdirAll(controlDir, 0700); err != nil {
		return procexec.OSRunner{}, "", err
	}
	if err := os.MkdirAll(filepath.Dir(knownHosts), 0700); err != nil {
		return procexec.OSRunner{}, "", err
	}
	path := environ["PATH"]
	if path == "" {
		path = os.Getenv("PATH")
	}
	env["PATH"] = path
	env["VMBOX_REAL_SSH"] = realSSH
	env["VMBOX_RAILWAY_KNOWN_HOSTS"] = knownHosts
	env["VMBOX_RAILWAY_CONTROL_DIR"] = controlDir
	return procexec.OSRunner{Env: env, Unset: unset}, knownHosts, nil
}
