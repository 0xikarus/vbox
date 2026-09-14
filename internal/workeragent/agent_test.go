package workeragent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

func TestConfigRequiresPrivateRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("accepted world-readable credentials")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil {
		t.Fatal("accepted symlinked config")
	}
}
func TestControllerEndpointRequiresTLSAndNoCredentials(t *testing.T) {
	for _, endpoint := range []string{"http://example.test", "https://user:password@example.test", "https://example.test?token=secret", "https://example.test#fragment"} {
		a := Agent{Config: Config{ControllerURL: endpoint}}
		if _, err := a.endpoint("/v1/workers/connect"); err == nil {
			t.Fatal("accepted unsafe endpoint")
		}
	}
}
func TestBindingChecksEveryIdentityAndReloadsAssignment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binding")
	expected := workerprotocol.Binding{AccountID: "account", SlotID: "slot", BoxID: "box", Assignment: "generation-1", Incarnation: "current"}
	data, _ := json.Marshal(expected)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	a := Agent{Config: Config{AccountID: "account", SlotID: "slot", BindingFile: path}, Incarnation: "current"}
	if err := a.validateBinding(context.Background(), expected); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*workerprotocol.Binding){
		func(b *workerprotocol.Binding) { b.AccountID = "other" }, func(b *workerprotocol.Binding) { b.SlotID = "other" },
		func(b *workerprotocol.Binding) { b.BoxID = "other" }, func(b *workerprotocol.Binding) { b.Assignment = "old" }, func(b *workerprotocol.Binding) { b.Incarnation = "old" },
	} {
		bad := expected
		change(&bad)
		if err := a.validateBinding(context.Background(), bad); err == nil {
			t.Fatal("accepted mismatched binding")
		}
	}
	next := expected
	next.Assignment = "generation-2"
	data, _ = json.Marshal(next)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.validateBinding(context.Background(), expected); err == nil {
		t.Fatal("cached stale assignment")
	}
	if err := a.validateBinding(context.Background(), next); err != nil {
		t.Fatal(err)
	}
}
