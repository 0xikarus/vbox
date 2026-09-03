package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestRailwayRunnerConfiguresDirectOpenSSHState(t *testing.T) {
	home := t.TempDir()
	path := os.Getenv("PATH")
	runner, knownHosts, err := railwayRunner("", "", map[string]string{"HOME": home, "PATH": path})
	if err != nil {
		t.Fatal(err)
	}
	controlDir := filepath.Join(home, ".local", "share", "vmbox", "railway-ssh", "control")
	if runner.Env["VMBOX_RAILWAY_CONTROL_DIR"] != controlDir {
		t.Fatalf("control directory=%q", runner.Env["VMBOX_RAILWAY_CONTROL_DIR"])
	}
	if runner.Env["VMBOX_REAL_SSH"] == "" || runner.Env["PATH"] != path {
		t.Fatalf("direct SSH runner environment=%v", runner.Env)
	}
	if knownHosts != filepath.Join(home, ".config", "vmbox", "railway-known-hosts") {
		t.Fatalf("known hosts=%q", knownHosts)
	}
	if info, err := os.Stat(controlDir); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("control directory info=%v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "vmbox", "railway-ssh", "ssh")); !os.IsNotExist(err) {
		t.Fatalf("legacy PATH-intercepting SSH wrapper still exists: %v", err)
	}
}

func TestRailwayTokenSelectionIsExclusive(t *testing.T) {
	token, environment, err := railwayToken(map[string]string{"RAILWAY_TOKEN": "project-token"})
	if err != nil || token != "project-token" || environment != "RAILWAY_TOKEN" {
		t.Fatalf("project token=%q environment=%q err=%v", token, environment, err)
	}
	if _, _, err := railwayToken(map[string]string{"RAILWAY_TOKEN": "project", "RAILWAY_API_TOKEN": "account"}); err == nil {
		t.Fatal("simultaneous Railway token types were accepted")
	}
}

func TestRailwayLocalCLIAuthRequiresExplicitContextOptIn(t *testing.T) {
	app := New()
	app.Environ = map[string]string{"HOME": t.TempDir()}
	base := config.Context{Provider: "railway", Project: "project", Environment: "environment"}
	if _, err := app.provider(base); err == nil {
		t.Fatal("missing Railway token was accepted without local auth opt-in")
	}
	base.RailwayCLIAuth = true
	if _, err := app.provider(base); err != nil {
		t.Fatalf("local Railway CLI auth opt-in rejected: %v", err)
	}
}
