package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/controller"
)

func TestRailwayControllerCredentialSelectsProjectToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	provider, err := providerForCredential("railway", controller.DecryptedProviderCredential{
		Secret:             json.RawMessage(`{"token":"project-secret"}`),
		ProviderCredential: v1.ProviderCredential{Config: json.RawMessage(`{"projectId":"project","environmentId":"environment","tokenEnvironment":"RAILWAY_TOKEN"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	value := reflect.ValueOf(provider).Elem().FieldByName("cfg").FieldByName("TokenEnvironment").String()
	if value != "RAILWAY_TOKEN" {
		t.Fatalf("token environment = %q", value)
	}
	knownHosts := reflect.ValueOf(provider).Elem().FieldByName("cfg").FieldByName("SSHKnownHostsFile").String()
	if knownHosts != filepath.Join(home, ".config", "vmbox", "railway-known-hosts") {
		t.Fatalf("known hosts = %q", knownHosts)
	}
	controlDir := filepath.Join(home, ".local", "share", "vmbox", "railway-ssh", "control")
	configuredControlDir := reflect.ValueOf(provider).Elem().FieldByName("cfg").FieldByName("SSHControlDir").String()
	if configuredControlDir != controlDir {
		t.Fatalf("SSH control directory = %q", configuredControlDir)
	}
	if info, err := os.Stat(controlDir); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("SSH control directory info=%v err=%v", info, err)
	}
	sshBinary := reflect.ValueOf(provider).Elem().FieldByName("cfg").FieldByName("SSHBinary").String()
	if sshBinary == "" {
		t.Fatal("direct OpenSSH binary was not configured")
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "vmbox", "railway-ssh", "ssh")); !os.IsNotExist(err) {
		t.Fatalf("legacy PATH-intercepting SSH wrapper still exists: %v", err)
	}
	if _, err := providerForCredential("railway", controller.DecryptedProviderCredential{
		Secret:             json.RawMessage(`{"token":"secret"}`),
		ProviderCredential: v1.ProviderCredential{Config: json.RawMessage(`{"tokenEnvironment":"BOTH"}`)},
	}); err == nil {
		t.Fatal("invalid Railway token environment was accepted")
	}
}

func TestSeedRailwayCredentialRequiresRailwayScopeWithoutLeakingToken(t *testing.T) {
	const token = "do-not-leak-project-token"
	t.Setenv("RAILWAY_TOKEN", token)
	t.Setenv("RAILWAY_PROJECT_ID", "")
	t.Setenv("RAILWAY_ENVIRONMENT_ID", "")
	err := seedRailwayCredentialFromEnvironment(context.Background(), &controller.Store{})
	if err == nil || !strings.Contains(err.Error(), "RAILWAY_PROJECT_ID") {
		t.Fatalf("error=%v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal("environment token leaked through error")
	}
}

func TestSeedRailwayCredentialIsDisabledWithoutToken(t *testing.T) {
	t.Setenv("RAILWAY_TOKEN", "")
	if err := seedRailwayCredentialFromEnvironment(context.Background(), &controller.Store{}); err != nil {
		t.Fatal(err)
	}
}
