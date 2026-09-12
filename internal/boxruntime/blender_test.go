package boxruntime

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlenderInstallsDesktopAndRestoresWithoutCustomScript(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "packages")
	t.Setenv("PATH", bin)
	t.Setenv("PACKAGE_LOG", log)
	t.Setenv("AGENT_GET_EXIT", "1")
	for name, script := range map[string]string{
		"apt-get": "#!/bin/sh\nexit 0\n",
		"sudo":    "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PACKAGE_LOG\"\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeBlenderToolFakes(t, bin)
	if err := InstallTools(context.Background(), home, []string{"blender"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := RestoreToolSetup(context.Background(), home, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	installLines := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "apt-get") && strings.Contains(line, "install --no-remove") {
			installLines = append(installLines, line)
		}
	}
	if len(installLines) != 2 {
		t.Fatalf("expected initial and restored package installs: %s", data)
	}
	for _, pkg := range []string{"blender", "pipx", "python3-venv", "tigervnc-standalone-server", "openbox", "firefox-esr"} {
		for _, line := range installLines {
			if !strings.Contains(line, pkg) {
				t.Fatalf("initial install and restore must include %s: %s", pkg, data)
			}
		}
	}
	if strings.Count(string(data), "pipx install --force blender-mcp=="+blenderMCPVersion) != 1 {
		t.Fatalf("pinned Blender MCP should install once: %s", data)
	}
	for _, expected := range []string{"blender-mcp install-addon --addons-dir " + filepath.Join(home, ".config", "blender", "3.4", "scripts", "addons"), "codex mcp add blender", "claude mcp add blender --scope user"} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("missing Blender MCP setup %q: %s", expected, data)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PACKAGE_LOG\"\ncase \"$*\" in *' check') exit 100;; esac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := RestoreToolSetup(context.Background(), home, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(log)
	if !strings.Contains(string(data), "--fix-broken --no-remove install") {
		t.Fatal("broken package dependencies were not repaired with removals forbidden")
	}
	if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte("#!/bin/sh\nexit 17\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := RestoreToolSetup(context.Background(), home, io.Discard); err == nil {
		t.Fatal("package failure must fail restoration")
	}
}

func TestBlenderAlreadyInstalledDoesNotInvokePackageManager(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "packages")
	t.Setenv("PATH", bin)
	t.Setenv("PACKAGE_LOG", log)
	t.Setenv("AGENT_GET_EXIT", "0")
	for _, name := range []string{"Xtigervnc", "openbox", "firefox-esr", "tint2", "pcmanfm", "xdg-user-dir"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeBlenderToolFakes(t, bin)
	if err := InstallTools(context.Background(), home, []string{"blender"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "mcp add") {
		t.Fatalf("existing agent MCP configuration was overwritten: %s", data)
	}
}

func writeBlenderToolFakes(t *testing.T, bin string) {
	t.Helper()
	scripts := map[string]string{
		"blender": "#!/bin/sh\nprintf 'blender %s\\n' \"$*\" >> \"$PACKAGE_LOG\"\nif [ \"$1\" = '--version' ]; then printf 'Blender 3.4.1\\n'; fi\n",
		"pipx": `#!/bin/sh
printf 'pipx %s\n' "$*" >> "$PACKAGE_LOG"
/bin/mkdir -p "$PIPX_BIN_DIR"
/bin/cp "$FAKE_BLENDER_MCP" "$PIPX_BIN_DIR/blender-mcp"
/bin/chmod 700 "$PIPX_BIN_DIR/blender-mcp"
`,
		"codex":  "#!/bin/sh\nprintf 'codex %s\\n' \"$*\" >> \"$PACKAGE_LOG\"\nif [ \"$2\" = 'get' ]; then [ \"$AGENT_GET_EXIT\" = 0 ] || printf 'No MCP server named blender\\n'; exit \"$AGENT_GET_EXIT\"; fi\n",
		"claude": "#!/bin/sh\nprintf 'claude %s\\n' \"$*\" >> \"$PACKAGE_LOG\"\nif [ \"$2\" = 'get' ]; then [ \"$AGENT_GET_EXIT\" = 0 ] || printf 'No MCP server named blender\\n'; exit \"$AGENT_GET_EXIT\"; fi\n",
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	server := filepath.Join(bin, "fake-blender-mcp")
	if err := os.WriteFile(server, []byte("#!/bin/sh\nprintf 'blender-mcp %s\\n' \"$*\" >> \"$PACKAGE_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_BLENDER_MCP", server)
}
