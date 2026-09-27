package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsEmptyGoConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.Current != "" || len(file.Contexts) != 0 || len(file.LastSetups) != 0 {
		t.Fatalf("missing configuration = %+v", file)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("loading a missing configuration unexpectedly created it: %v", err)
	}
}

func TestSaveAndLoadGoConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	existing := File{
		Current: "ubuntu",
		Contexts: map[string]Context{
			"ubuntu": {Provider: "incus", IncusRemote: "host"},
		},
		LastSetups: map[string]CreationSetup{},
	}
	if err := Save(path, existing); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Current != "ubuntu" || loaded.Contexts["ubuntu"].Provider != "incus" {
		t.Fatalf("loaded configuration = %+v", loaded)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}
}

func TestConnectedIgnoresLegacyProviderSelection(t *testing.T) {
	file := File{Current: "team", Contexts: map[string]Context{
		"team": {Controller: "https://controller.example", TokenEnv: "TEAM_TOKEN", Provider: "railway", ProviderCredential: "primary"},
	}}
	connected, err := file.Connected()
	if err != nil || connected.Name != "team" || connected.Controller != "https://controller.example" || connected.Provider != "" || connected.ProviderCredential != "" {
		t.Fatalf("legacy connection: %+v, %v", connected, err)
	}
	file.Connection = &ControllerConnection{Controller: "https://new.example", TokenEnv: "NEW_TOKEN"}
	connected, err = file.Connected()
	if err != nil || connected.Name != "" || connected.Controller != "https://new.example" || connected.TokenEnv != "NEW_TOKEN" {
		t.Fatalf("single connection: %+v, %v", connected, err)
	}
}
