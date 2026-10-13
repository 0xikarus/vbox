package boxruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoalTaskFilesConfinedToWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("VMBOX_WORKSPACE_ROOT", root)
	if err := os.Mkdir(filepath.Join(root, "workspace"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "report.txt"), []byte("report"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "private"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../private", filepath.Join(root, "workspace", "escape")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../private", filepath.Join(root, "private"), "escape", "."} {
		if _, _, err := readGoalTaskFile(path); err == nil {
			t.Fatalf("accepted outside/non-file path %q", path)
		}
	}
	for _, path := range []string{"report.txt", filepath.Join(root, "workspace", "report.txt")} {
		name, data, err := readGoalTaskFile(path)
		if err != nil || name != "report.txt" || string(data) != "report" {
			t.Fatal(name, string(data), err)
		}
	}
	f, err := os.Create(filepath.Join(root, "workspace", "large"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate((25 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err = readGoalTaskFile("large"); err == nil {
		t.Fatal("accepted oversized result")
	}
}
