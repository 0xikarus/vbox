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
