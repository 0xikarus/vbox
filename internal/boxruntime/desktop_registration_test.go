package boxruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestOpenCodeDesktopRegistrationPreservesSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OPENCODE_CONFIG", "")
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "opencode.json")
	original := []byte(`{"model":"custom/model","mcp":{"existing":{"type":"local","command":["example"]}}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := RegisterDesktopMCP(context.Background(), home, "opencode"); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model string                     `json:"model"`
		MCP   map[string]json.RawMessage `json:"mcp"`
	}
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "custom/model" || len(got.MCP) != 2 || got.MCP["existing"] == nil || got.MCP["vmbox-desktop"] == nil {
		t.Fatal("configuration not preserved")
	}
	if err = os.WriteFile(path, []byte(`{"mcp":{"vmbox-desktop":{"type":"local","command":["vmbox-runtime","desktop-mcp"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = RegisterDesktopMCP(context.Background(), home, "opencode"); err != nil {
		t.Fatal(err)
	}
	migrated, _ := os.ReadFile(path)
	if !strings.Contains(string(migrated), managedDesktopRuntimePath) {
		t.Fatal("legacy OpenCode runtime path was not migrated")
	}
	if err = os.WriteFile(path, []byte(`{"mcp":{"vmbox-desktop":{"disabled":true}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err = RegisterDesktopMCP(context.Background(), home, "opencode"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("overwrote existing desktop configuration")
	}
}

func TestDesktopRegistrationCancellationDoesNotModifyConfig(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RegisterDesktopMCP(ctx, home, "opencode"); err != context.Canceled {
		t.Fatalf("canceled registration: %v", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("canceled registration modified home")
	}
}

func TestDesktopRegistrationCLIPreservesExistingAndRejectsUnexpectedFailure(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		for _, mode := range []string{"exists", "missing", "legacy", "failure"} {
			t.Run(agent+"/"+mode, func(t *testing.T) {
				home := t.TempDir()
				bin := t.TempDir()
				script := `#!/bin/sh
if [ "$1 $2 $3" = "mcp get vmbox-desktop" ]; then
 case "$REGISTRATION_TEST_MODE" in
 exists) exit 0;;
 missing) echo 'No MCP server named vmbox-desktop found'; exit 1;;
 legacy) printf 'Command: vmbox-runtime\nArgs: desktop-mcp\n'; exit 0;;
 failure) echo 'synthetic-private-value'; exit 1;;
 esac
fi
printf '%s\n' "$@" >> "$HOME/registration-args"
`
				if err := os.WriteFile(filepath.Join(bin, agent), []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin)
				t.Setenv("REGISTRATION_TEST_MODE", mode)
				err := RegisterDesktopMCP(context.Background(), home, agent)
				if mode == "failure" {
					if err == nil || strings.Contains(err.Error(), "synthetic-private-value") {
						t.Fatal("unexpected inspection failure must be private and fail closed")
					}
				} else if err != nil {
					t.Fatal(err)
				}
				args, readErr := os.ReadFile(filepath.Join(home, "registration-args"))
				if mode != "missing" && mode != "legacy" && !(mode == "exists" && agent == "codex") {
					if !os.IsNotExist(readErr) {
						t.Fatal("registration changed after existing entry or inspection failure")
					}
					return
				}
				want := ""
				if mode == "legacy" || (mode == "exists" && agent == "codex") {
					want = "mcp\nremove\nvmbox-desktop\n"
					if agent == "claude" {
						want += "--scope\nuser\n"
					}
				}
				want += "mcp\nadd\nvmbox-desktop\n"
				if agent == "claude" {
					want += "--scope\nuser\n"
				}
				// Codex sanitizes the environment it gives an MCP server, so the
				// variables the desktop server needs are passed explicitly.
				if agent == "codex" {
					for _, key := range desktopMCPEnvironment {
						if value := os.Getenv(key); value != "" {
							want += "--env\n" + key + "=" + value + "\n"
						}
					}
				}
				want += "--\n" + managedDesktopRuntimePath + "\ndesktop-mcp\n"
				if readErr != nil || string(args) != want {
					t.Fatalf("registration argv: %q, %v", args, readErr)
				}
			})
		}
	}
}

func TestPrepareManagedDesktopRegistersChatMCPWithoutDesktopPackages(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	script := `#!/bin/sh
if [ "$1 $2 $3" = "mcp get vmbox-desktop" ]; then
 echo 'No MCP server named vmbox-desktop found'
 exit 1
fi
printf '%s\n' "$@" > "$HOME/registration-args"
`
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	if assignment, err := prepareManagedDesktop(context.Background(), "codex"); err != nil || assignment != "" {
		t.Fatalf("assignment=%q err=%v", assignment, err)
	}
	if _, err := os.Stat(filepath.Join(home, "registration-args")); err != nil {
		t.Fatal("chat MCP was not registered on a shell-only image:", err)
	}
}

func TestDesktopRegistrationLockWaitIsCancelable(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "vmbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "mcp-registration.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	if err = RegisterDesktopMCP(ctx, home, "opencode"); err != context.DeadlineExceeded {
		t.Fatalf("lock wait: %v", err)
	}
	if _, err = os.Stat(filepath.Join(home, ".config", "opencode")); !os.IsNotExist(err) {
		t.Fatal("registration ran while lock was held")
	}
}

func TestOpenCodeExplicitConfigIgnoresUnrelatedJSONC(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "custom.json")
	t.Setenv("OPENCODE_CONFIG", path)
	if err := os.WriteFile(filepath.Join(home, "opencode.jsonc"), []byte("// unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RegisterDesktopMCP(context.Background(), home, "opencode"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "vmbox-desktop") {
		t.Fatal("explicit config not registered")
	}
}
