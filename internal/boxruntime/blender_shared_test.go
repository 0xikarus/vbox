package boxruntime

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSharedBlenderRequiresBundledImageWithoutSudo(t *testing.T) {
	if _, err := os.Stat("/opt/vmbox/blender-" + blenderVersion + "/blender"); err == nil {
		t.Skip("requires a host without bundled Blender")
	}
	t.Setenv("VMBOX_WORKSPACE_ROOT", t.TempDir())
	err := installPinnedBlender(context.Background(), t.TempDir(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "shared worker image must include") {
		t.Fatalf("unexpected missing-image result: %v", err)
	}
}

func TestSharedOpenCodeBlenderUsesWorkspacePort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VMBOX_WORKSPACE_ROOT", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("OPENCODE_CONFIG", "")
	if err := registerOpenCodeBlender(home, "/blender-mcp"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCP map[string]struct {
			Environment map[string]string `json:"environment"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.MCP["blender"].Environment["BLENDER_PORT"] != strconv.Itoa(os.Getuid()) {
		t.Fatal("OpenCode port does not match shared workspace")
	}
}

func TestBlenderMCPPortUsesWorkspaceIdentity(t *testing.T) {
	t.Setenv("VMBOX_WORKSPACE_ROOT", "/data")
	if blenderMCPPort() != "9876" {
		t.Fatal("dedicated port changed")
	}
	t.Setenv("VMBOX_WORKSPACE_ROOT", t.TempDir())
	if blenderMCPPort() != strconv.Itoa(os.Getuid()) {
		t.Fatal("shared port does not match workspace UID")
	}
}

func TestSharedBlenderAddonPinsLoopbackAndScenePort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blender_mcp.py")
	source := "        self.host = host\n        self.port = port\n        default=9876,\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := configureSharedBlenderAddon(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"self.host = '127.0.0.1'", "self.port = os.getuid()", "get=lambda self: os.getuid()", "set=lambda self, value: None"} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("missing %s", expected)
		}
	}
}

func TestSharedBlenderAddonRejectsUnknownSourceWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blender_mcp.py")
	source := "unexpected upstream source"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := configureSharedBlenderAddon(path); err == nil {
		t.Fatal("accepted incompatible add-on")
	}
	data, _ := os.ReadFile(path)
	if string(data) != source {
		t.Fatal("changed incompatible add-on")
	}
}
