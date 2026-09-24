package boxruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceFoldersMirrorToDesktopWithoutChangingOwnerFiles(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	desktop := filepath.Join(root, "Custom Desktop")
	manifest := filepath.Join(root, ".config", "vmbox", "workspace-desktop-folders.json")
	for _, path := range []string{workspace, desktop, filepath.Join(workspace, "Project Alpha"), filepath.Join(workspace, "Existing"), filepath.Join(workspace, ".hidden")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ownerFile := filepath.Join(desktop, "Existing")
	if err := os.WriteFile(ownerFile, []byte("owner file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(workspace, "Project Alpha"), filepath.Join(workspace, "folder alias")); err != nil {
		t.Fatal(err)
	}
	sync := func() {
		t.Helper()
		if err := syncWorkspaceDesktopFolders(workspace, desktop, manifest); err != nil {
			t.Fatal(err)
		}
	}
	sync()
	link := filepath.Join(desktop, "Project Alpha")
	if target, err := os.Readlink(link); err != nil || target != filepath.Join(workspace, "Project Alpha") {
		t.Fatalf("workspace folder link=%q, error=%v", target, err)
	}
	for _, name := range []string{".hidden", "folder alias"} {
		if _, err := os.Lstat(filepath.Join(desktop, name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected desktop link %q: %v", name, err)
		}
	}
	if data, _ := os.ReadFile(ownerFile); string(data) != "owner file" {
		t.Fatalf("owner file changed: %q", data)
	}
	if err := os.Rename(filepath.Join(workspace, "Project Alpha"), filepath.Join(workspace, "New Project")); err != nil {
		t.Fatal(err)
	}
	sync()
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("stale desktop link remains: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(desktop, "New Project")); err != nil || target != filepath.Join(workspace, "New Project") {
		t.Fatalf("new workspace folder link=%q, error=%v", target, err)
	}
	if err := os.Remove(filepath.Join(desktop, "New Project")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(desktop, "New Project"), []byte("owner replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	sync()
	if data, _ := os.ReadFile(filepath.Join(desktop, "New Project")); string(data) != "owner replacement" {
		t.Fatalf("owner replacement changed: %q", data)
	}
}
