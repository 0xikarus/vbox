package boxruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedWorkspacePaths(t *testing.T) {
	isolateRuntimeEnv(t)
	root := t.TempDir()
	t.Setenv("VMBOX_WORKSPACE_ROOT", root)
	t.Setenv("VMBOX_DESKTOP_DISPLAY", ":102")
	if err := ValidateWorkspaceEnvironment(); err != nil && os.Geteuid() != 0 {
		t.Fatal(err)
	}
	if WorkspaceDirectory() != root+"/workspace" || WorkloadHome() != root+"/home" || New("").Root != root+"/.vmbox" || DesktopDisplay() != ":102" {
		t.Fatal("shared runtime paths escaped their workspace")
	}
	if WorkspacePath("/data/home/.codex/auth.json") != root+"/home/.codex/auth.json" || WorkspacePath("/database") != "/database" {
		t.Fatal("incorrect control-plane path translation")
	}
	command, args := WorkloadArgv(32001, "gh", []string{"auth", "status"})
	if command != "env" || args[0] != "HOME="+root+"/home" {
		t.Fatal("shared workload authentication uses another home")
	}
	if err := RegisterDesktopMCP(context.Background(), WorkloadHome(), "opencode"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(WorkloadHome(), ".config", "opencode", "opencode.json"))
	if err != nil || !json.Valid(data) || !strings.Contains(string(data), root+"/home/bin/vmbox-runtime") {
		t.Fatalf("MCP runtime is not workspace-local: %v", err)
	}
}

func TestInvalidWorkspaceEnvironment(t *testing.T) {
	for _, root := range []string{"relative", "/data/../etc", "/data\nother"} {
		t.Setenv("VMBOX_WORKSPACE_ROOT", root)
		if ValidateWorkspaceEnvironment() == nil {
			t.Fatalf("accepted invalid workspace %q", root)
		}
	}
	t.Setenv("VMBOX_WORKSPACE_ROOT", "/data")
	for _, display := range []string{"remote:99", ":0", ":-1", ":65536", ":1;id"} {
		t.Setenv("VMBOX_DESKTOP_DISPLAY", display)
		if ValidateWorkspaceEnvironment() == nil {
			t.Fatalf("accepted invalid display %q", display)
		}
	}
}
