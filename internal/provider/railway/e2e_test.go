package railway

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestRailwayE2E(t *testing.T) {
	if os.Getenv("VMBOX_E2E_RAILWAY") != "1" {
		t.Skip("set VMBOX_E2E_RAILWAY=1 with a scoped Railway token")
	}
	tokenEnvironment := os.Getenv("VMBOX_RAILWAY_TOKEN_ENVIRONMENT")
	if tokenEnvironment == "" {
		tokenEnvironment = "RAILWAY_TOKEN"
	}
	token := os.Getenv(tokenEnvironment)
	projectID := os.Getenv("VMBOX_RAILWAY_PROJECT_ID")
	environmentID := os.Getenv("VMBOX_RAILWAY_ENVIRONMENT_ID")
	if token == "" || projectID == "" || environmentID == "" {
		t.Fatal("Railway token, VMBOX_RAILWAY_PROJECT_ID, and VMBOX_RAILWAY_ENVIRONMENT_ID are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	name := "e2e-" + strings.ToLower(time.Now().UTC().Format("150405"))
	owner := provider.Owner{AccountID: "e2e", BoxID: name, Lease: "railway-e2e"}
	p := New(Config{
		ProjectID:        projectID,
		EnvironmentID:    environmentID,
		Token:            token,
		TokenEnvironment: tokenEnvironment,
		DefaultImage:     "nginx:1.27-alpine",
		PollInterval:     2 * time.Second,
		ReadyTimeout:     15 * time.Minute,
	}, procexec.OSRunner{})
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Minute)
		defer done()
		if err := p.Delete(cleanup, name, owner); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	if _, err := p.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	box, err := p.Create(ctx, provider.CreateRequest{
		Name: name, Image: "nginx:1.27-alpine", Region: "eu-west",
		Resources: provider.Resources{CPU: 1, MemoryMiB: 512, DiskGiB: 1}, Owner: owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if box.State != provider.StateRunning || box.Owner != owner || box.Storage == nil || box.Storage.MountPath != "/data" {
		t.Fatalf("created box = %+v", box)
	}
	if box.Resources.CPU != 1 || box.Resources.MemoryMiB != 512 {
		t.Fatalf("created resources = %+v", box.Resources)
	}
	stopped, err := p.Stop(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.State != provider.StateStopped {
		t.Fatalf("stopped state = %s (%s)", stopped.State, stopped.ProviderState)
	}
	started, err := p.Start(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if started.State != provider.StateRunning {
		t.Fatalf("started state = %s (%s)", started.State, started.ProviderState)
	}
	resized, err := p.Resize(ctx, name, provider.Resources{CPU: 2, MemoryMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if resized.Resources.CPU != 2 || resized.Resources.MemoryMiB != 1024 {
		t.Fatalf("resized resources = %+v", resized.Resources)
	}
}
