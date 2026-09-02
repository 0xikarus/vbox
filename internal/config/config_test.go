package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMigratesLegacyRailwayTargetWithoutCredentials(t *testing.T) {
	directory := t.TempDir()
	legacyPath := filepath.Join(directory, "config")
	jsonPath := filepath.Join(directory, "config.json")
	legacy := []byte(`VMBOX_PROJECT_ID="project-123"
VMBOX_ENVIRONMENT_ID='environment-456'
VMBOX_DEFAULT_REGION="us-east"
RAILWAY_API_TOKEN="must-not-migrate"
IGNORED_COMMAND="$(touch should-never-run)"
`)
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	file, err := Load(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if file.Current != "railway" || file.MigratedFrom != legacyPath {
		t.Fatalf("migration metadata = %+v", file)
	}
	context := file.Contexts["railway"]
	if context.Provider != "railway" || context.Project != "project-123" || context.Environment != "environment-456" {
		t.Fatalf("migrated context = %+v", context)
	}
	if !context.RailwayCLIAuth || context.Cluster != "iad" {
		t.Fatalf("Railway compatibility settings = %+v", context)
	}
	persisted, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("must-not-migrate"), []byte("touch should-never-run"), []byte("RAILWAY_API_TOKEN")} {
		if bytes.Contains(persisted, forbidden) {
			t.Fatalf("persisted config contains forbidden legacy content %q", forbidden)
		}
	}
	info, err := os.Stat(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}

	reloaded, err := Load(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.MigratedFrom != "" || reloaded.Current != "railway" {
		t.Fatalf("reloaded config = %+v", reloaded)
	}
}

func TestLoadKeepsExistingGoContext(t *testing.T) {
	directory := t.TempDir()
	jsonPath := filepath.Join(directory, "config.json")
	existing := File{Current: "ubuntu", Contexts: map[string]Context{"ubuntu": {Provider: "incus", IncusRemote: "host"}}}
	if err := Save(jsonPath, existing); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "config"), []byte("VMBOX_PROJECT_ID=old\nVMBOX_ENVIRONMENT_ID=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Current != "ubuntu" || loaded.Contexts["ubuntu"].Provider != "incus" || loaded.MigratedFrom != "" {
		t.Fatalf("existing context was replaced: %+v", loaded)
	}
}
