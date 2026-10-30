package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStandaloneDirectoryCleanupVerifiesOwnershipAndRemovesAllContents(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "owned-instance")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	i := standaloneInstance{ID: "sa-test-cleanup", Directory: dir}
	data, _ := json.Marshal(i)
	if err := os.WriteFile(filepath.Join(dir, "instance.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "logs", "test.log"), []byte("disposable test log"), 0600); err != nil {
		t.Fatal(err)
	}
	a := New()
	wrong := i
	wrong.ID = "another-instance"
	if a.cleanupStandaloneDirectory(context.Background(), wrong, false) == nil {
		t.Fatal("foreign directory removed")
	}
	if err := a.cleanupStandaloneDirectory(context.Background(), i, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("preflight removed data")
	}
	if err := a.cleanupStandaloneDirectory(context.Background(), i, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("instance directory remains")
	}
	if err := a.cleanupStandaloneDirectory(context.Background(), i, false); err != nil {
		t.Fatal("cleanup retry failed", err)
	}
}
