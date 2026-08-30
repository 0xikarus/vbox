package components

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoveryNeverIncludesMarkdown(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"auth.json", "config.toml", "AGENTS.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	profiles, err := Discover(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || len(profiles[0].Files) != 2 {
		t.Fatalf("profiles=%+v", profiles)
	}
	for _, path := range profiles[0].Files {
		if filepath.Ext(path) == ".md" {
			t.Fatalf("Markdown copied as auth: %s", path)
		}
	}
}

func TestDiscoverFindsNamedProfiles(t *testing.T) {
	home := t.TempDir()
	for _, path := range []string{
		filepath.Join(home, ".codex-two", "auth.json"),
		filepath.Join(home, ".claude-work", ".credentials.json"),
		filepath.Join(home, ".config", "opencode-team", "auth.json"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	profiles, err := Discover(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 3 {
		t.Fatalf("profiles=%+v", profiles)
	}
}
