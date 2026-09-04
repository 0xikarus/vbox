package main

import (
	"context"
	"encoding/base64"
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
	}, "/run/secrets/controller-ssh")
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
	identityFile := reflect.ValueOf(provider).Elem().FieldByName("cfg").FieldByName("SSHIdentityFile").String()
	if identityFile != "/run/secrets/controller-ssh" {
		t.Fatalf("SSH identity file = %q", identityFile)
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
	}, ""); err == nil {
		t.Fatal("invalid Railway token environment was accepted")
	}
}

func TestMaterializeRailwaySSHIdentity(t *testing.T) {
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nprivate material\n-----END OPENSSH PRIVATE KEY-----\n"
	path, err := materializeRailwaySSHIdentity(base64.StdEncoding.EncodeToString([]byte(key)), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != key {
		t.Fatal("materialized SSH key differs from input")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("SSH identity info=%v err=%v", info, err)
	}
}

func TestMaterializeRailwaySSHIdentityRejectsInvalidInput(t *testing.T) {
	for _, encoded := range []string{"not base64", base64.StdEncoding.EncodeToString([]byte("not a key"))} {
		if _, err := materializeRailwaySSHIdentity(encoded, t.TempDir()); err == nil {
			t.Fatalf("accepted invalid identity %q", encoded)
		}
	}
	if path, err := materializeRailwaySSHIdentity("", t.TempDir()); err != nil || path != "" {
		t.Fatalf("empty identity path=%q err=%v", path, err)
	}
}

func TestSeedRailwayCredentialRequiresRailwayScopeWithoutLeakingToken(t *testing.T) {
	const token = "do-not-leak-project-token"
	t.Setenv("RAILWAY_API_TOKEN", "")
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

func TestSeedRailwayCredentialPrefersAccountOrWorkspaceToken(t *testing.T) {
	t.Setenv("RAILWAY_API_TOKEN", "account-or-workspace-token")
	t.Setenv("RAILWAY_TOKEN", "project-token")
	t.Setenv("RAILWAY_PROJECT_ID", "")
	t.Setenv("RAILWAY_ENVIRONMENT_ID", "")
	err := seedRailwayCredentialFromEnvironment(context.Background(), &controller.Store{})
	if err == nil || !strings.Contains(err.Error(), "RAILWAY_API_TOKEN requires") {
		t.Fatalf("error=%v", err)
	}
	if strings.Contains(err.Error(), "account-or-workspace-token") || strings.Contains(err.Error(), "project-token") {
		t.Fatal("environment token leaked through error")
	}
}

func TestSeedRailwayCredentialIsDisabledWithoutToken(t *testing.T) {
	t.Setenv("RAILWAY_API_TOKEN", "")
	t.Setenv("RAILWAY_TOKEN", "")
	if err := seedRailwayCredentialFromEnvironment(context.Background(), &controller.Store{}); err != nil {
		t.Fatal(err)
	}
}

func TestSeedInitialFleetIsDisabledWithoutSetting(t *testing.T) {
	t.Setenv("VMBOX_INITIAL_COMPUTE_BOX_SLOTS", "")
	if err := seedInitialFleetFromEnvironment(context.Background(), &controller.Store{}); err != nil {
		t.Fatal(err)
	}
}

func TestSeedInitialFleetRejectsInvalidSetting(t *testing.T) {
	t.Setenv("VMBOX_INITIAL_COMPUTE_BOX_SLOTS", "two")
	err := seedInitialFleetFromEnvironment(context.Background(), &controller.Store{})
	if err == nil || !strings.Contains(err.Error(), "must be an integer") {
		t.Fatalf("error=%v", err)
	}
}
