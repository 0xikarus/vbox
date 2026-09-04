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

func TestDiscoverConfiguredMarksActiveClaudeProfileAndCopiesHomeState(t *testing.T) {
	home := t.TempDir()
	defaultProfile := filepath.Join(home, ".claude")
	namedProfile := filepath.Join(home, ".claude-20")
	for path, content := range map[string]string{
		filepath.Join(defaultProfile, ".credentials.json"): "default",
		filepath.Join(defaultProfile, ".claude.json"):      "unsupported-duplicate",
		filepath.Join(home, ".claude.json"):                "home-state",
		filepath.Join(namedProfile, ".credentials.json"):   "named",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	profiles, err := DiscoverConfigured(home, map[string]string{"CLAUDE_CONFIG_DIR": namedProfile})
	if err != nil {
		t.Fatal(err)
	}
	var defaults, named Profile
	for _, profile := range profiles {
		if profile.Component != "claude" {
			continue
		}
		switch profile.Directory {
		case defaultProfile:
			defaults = profile
		case namedProfile:
			named = profile
		}
	}
	if !named.Active || defaults.Active {
		t.Fatalf("default=%+v named=%+v", defaults, named)
	}
	if len(defaults.Files) != 2 || defaults.Files[1] != filepath.Join(home, ".claude.json") {
		t.Fatalf("default Claude files=%#v", defaults.Files)
	}
	for _, path := range defaults.Files {
		if path == filepath.Join(defaultProfile, ".claude.json") {
			t.Fatalf("nested Claude state would duplicate the home destination: %#v", defaults.Files)
		}
	}
	selected, err := ProfileAt("claude", defaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Files) != 2 || selected.Files[1] != filepath.Join(home, ".claude.json") {
		t.Fatalf("explicit Claude files=%#v", selected.Files)
	}
}
