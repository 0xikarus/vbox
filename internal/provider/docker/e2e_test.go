package docker

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestDockerE2E(t *testing.T) {
	if os.Getenv("VMBOX_E2E_DOCKER") != "1" {
		t.Skip("set VMBOX_E2E_DOCKER=1 with a Docker daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	name := "e2e-" + strings.ToLower(time.Now().Format("150405"))
	owner := provider.Owner{AccountID: "e2e", BoxID: name, Lease: "test-lease"}
	p := New(Config{DefaultImage: envOr("VMBOX_E2E_IMAGE", "vmbox:e2e")}, procexec.OSRunner{})
	box, err := p.Create(ctx, provider.CreateRequest{Name: name, Owner: owner, Resources: provider.Resources{CPU: 1, MemoryMiB: 256, DiskGiB: 1}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		if err := p.Delete(cleanup, name, owner); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	if box.State != provider.StateRunning {
		t.Fatalf("state=%s", box.State)
	}
	argv := []string{"printf", "%s", `$HOME; $(touch /tmp/not-run)`}
	if box.ID != name {
		t.Fatalf("stable box ID=%q want=%q", box.ID, name)
	}
	result, err := p.Exec(ctx, name, argv, provider.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != argv[2] {
		t.Fatalf("argv changed: %q", result.Stdout)
	}
	if _, err := p.Exec(ctx, name, []string{"sh", "-c", "printf persisted >/data/e2e"}, provider.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stop(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	result, err = p.Exec(ctx, name, []string{"cat", "/data/e2e"}, provider.ExecOptions{})
	if err != nil || result.Stdout != "persisted" {
		t.Fatalf("persistence stdout=%q err=%v", result.Stdout, err)
	}
	detached, err := p.Exec(ctx, name, []string{"sh", "-c", "sleep 0.2; printf detached >/data/e2e-detached"}, provider.ExecOptions{Detach: true})
	if err != nil || !strings.HasPrefix(strings.TrimSpace(detached.Stdout), "run_") {
		t.Fatalf("detached run id=%q err=%v", detached.Stdout, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		result, err = p.Exec(ctx, name, []string{"cat", "/data/e2e-detached"}, provider.ExecOptions{})
		if err == nil && result.ExitCode == 0 && result.Stdout == "detached" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("detached command did not finish: stdout=%q err=%v", result.Stdout, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

}
func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
