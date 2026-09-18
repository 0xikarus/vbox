package boxruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCodexDefaultsWritesPermissionsAndTrust(t *testing.T) {
	home := t.TempDir()
	if err := EnsureCodexDefaults(home, "/data/workspaces/box/workspace"); err != nil {
		t.Fatal(err)
	}
	config := readCodexConfig(t, home)
	for _, want := range []string{
		`approval_policy = "never"`,
		`sandbox_mode = "danger-full-access"`,
		`[projects."/data/workspaces/box/workspace"]`,
		`trust_level = "trusted"`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("missing %q in:\n%s", want, config)
		}
	}
}

// An imported login profile ships its own config.toml. Applying the box defaults
// over it must keep the user's settings and their other trusted projects.
func TestEnsureCodexDefaultsPreservesImportedProfile(t *testing.T) {
	home := t.TempDir()
	imported := `model = "gpt-5.6-sol"
model_reasoning_effort = "high"

[projects."/home/user/other"]
trust_level = "trusted"

[tui.model_availability_nux]
gpt-6-astra = 2
`
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(imported), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCodexDefaults(home, "/data/workspaces/box/workspace"); err != nil {
		t.Fatal(err)
	}
	config := readCodexConfig(t, home)
	for _, want := range []string{
		`model = "gpt-5.6-sol"`,
		`model_reasoning_effort = "high"`,
		`[projects."/home/user/other"]`,
		`[tui.model_availability_nux]`,
		`approval_policy = "never"`,
		`sandbox_mode = "danger-full-access"`,
		`[projects."/data/workspaces/box/workspace"]`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("missing %q in:\n%s", want, config)
		}
	}
}

// Re-applying must not accumulate duplicates: it runs on every attach.
func TestEnsureCodexDefaultsIsIdempotent(t *testing.T) {
	home := t.TempDir()
	workspace := "/data/workspaces/box/workspace"
	for range 3 {
		if err := EnsureCodexDefaults(home, workspace); err != nil {
			t.Fatal(err)
		}
	}
	config := readCodexConfig(t, home)
	for _, key := range []string{`approval_policy = "never"`, `sandbox_mode = "danger-full-access"`, `[projects."` + workspace + `"]`} {
		if got := strings.Count(config, key); got != 1 {
			t.Fatalf("%q appears %d times in:\n%s", key, got, config)
		}
	}
}
func readCodexConfig(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Codex appends its own tables, so the workspace table stops being last. The
// trust marker used to be appended again, and Codex then refused to parse its
// own configuration: "duplicate key".
func TestEnsureCodexDefaultsKeepsOneProjectTable(t *testing.T) {
	home := t.TempDir()
	workspace := "/data/workspace"
	if err := EnsureCodexDefaults(home, workspace); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".codex", "config.toml")
	appended, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Codex writes this itself after its first run.
	if err := os.WriteFile(path, append(appended, []byte("\n[tui.model_availability_nux]\ngpt-6-astra = 2\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := EnsureCodexDefaults(home, workspace); err != nil {
			t.Fatal(err)
		}
	}
	final, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	header := `[projects."/data/workspace"]`
	if count := strings.Count(string(final), header); count != 1 {
		t.Fatalf("workspace table appears %d times:\n%s", count, final)
	}
	for _, want := range []string{"gpt-6-astra = 2", "[tui.model_availability_nux]", `trust_level = "trusted"`} {
		if !strings.Contains(string(final), want) {
			t.Fatalf("lost %q:\n%s", want, final)
		}
	}
}

// Boxes already carry the duplicate this bug wrote, so the repair has to run on
// what is on disk rather than only preventing the next one.
func TestEnsureCodexDefaultsRepairsAnExistingDuplicate(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	broken := `approval_policy = "never"
sandbox_mode = "danger-full-access"

[mcp_servers.vmbox-desktop]
command = "/data/home/bin/vmbox-runtime"

[projects."/data/workspace"]
trust_level = "trusted"

[tui.model_availability_nux]
gpt-6-astra = 2

[projects."/data/workspace"]
trust_level = "trusted"
`
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCodexDefaults(home, "/data/workspace"); err != nil {
		t.Fatal(err)
	}
	final, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(final), `[projects."/data/workspace"]`); count != 1 {
		t.Fatalf("duplicate not repaired (%d tables):\n%s", count, final)
	}
	for _, want := range []string{"[mcp_servers.vmbox-desktop]", "gpt-6-astra = 2", `trust_level = "trusted"`} {
		if !strings.Contains(string(final), want) {
			t.Fatalf("repair lost %q:\n%s", want, final)
		}
	}
}
