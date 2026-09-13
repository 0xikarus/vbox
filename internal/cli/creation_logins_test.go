package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestCreationDiscoversAllSupportedLoginsWithoutReadingSecrets(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(t.TempDir(), "selected-codex")
	for _, p := range []struct{ dir, file string }{{filepath.Join(home, ".claude"), ".credentials.json"}, {filepath.Join(home, ".claude-work"), ".credentials.json"}, {filepath.Join(home, ".codex"), "auth.json"}, {custom, "auth.json"}, {filepath.Join(home, ".local", "share", "opencode"), "auth.json"}, {filepath.Join(home, ".config", "opencode-work"), "auth.json"}, {filepath.Join(home, ".codex-config-only"), "config.toml"}} {
		if err := os.MkdirAll(p.dir, 0700); err != nil {
			t.Fatal(err)
		}
		// Deliberately invalid JSON: discovery must not parse or display it.
		if err := os.WriteFile(filepath.Join(p.dir, p.file), []byte("synthetic-secret-not-for-display"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a := New()
	a.Environ = map[string]string{"HOME": home, "CODEX_HOME": custom, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude-work")}
	profiles, err := a.discoverCreationLogins([]v1.LoginProfile{{Application: "codex", Name: "personal"}, {Application: "codex", Name: "team"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		if p.app == "github" {
			continue
		} // GitHub accounts are discovered through gh, not directory scans.
		if len(p.localPaths) != 2 {
			t.Fatalf("%s: got %d local profiles", p.app, len(p.localPaths))
		}
		if p.selection.Value != "Skip" || p.uploadPath() != "" {
			t.Fatal("discovery selected an upload without consent")
		}
		for label, path := range p.localPaths {
			if strings.Contains(label, "synthetic-secret") || strings.Contains(label, "config-only") {
				t.Fatal("secret or config-only profile exposed")
			}
			p.selection.Value = label
			if p.uploadPath() != path || !p.name.When() || p.path.When() {
				t.Fatal("detected choice did not map to upload path")
			}
		}
		if p.app == "codex" {
			if p.name.Value != "personal-2" || p.path.Value != custom || !strings.Contains(strings.Join(p.selection.Choices, "\n"), "Saved: team") {
				t.Fatal("active directory or saved choices missing")
			}
		}
	}
}

func TestCreationLoginExplicitSavedSelectionSurvivesDiscovery(t *testing.T) {
	a := New()
	a.Environ = map[string]string{"HOME": t.TempDir()}
	profiles, err := a.discoverCreationLogins(nil, []v1.LoginProfileRef{{Application: "claude", Name: "chosen"}})
	if err != nil {
		t.Fatal(err)
	}
	if profiles[0].selection.Value != "Saved: chosen" || profiles[0].uploadPath() != "" {
		t.Fatal("explicit saved choice changed")
	}
}
