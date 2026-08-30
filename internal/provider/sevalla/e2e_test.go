package sevalla

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestSevallaE2E(t *testing.T) {
	if os.Getenv("VMBOX_E2E_SEVALLA") != "1" {
		t.Skip("set VMBOX_E2E_SEVALLA=1 with a scoped Sevalla API token")
	}
	token := os.Getenv("SEVALLA_API_TOKEN")
	cluster := os.Getenv("VMBOX_SEVALLA_CLUSTER_ID")
	if token == "" || cluster == "" {
		t.Fatal("SEVALLA_API_TOKEN and VMBOX_SEVALLA_CLUSTER_ID are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	name := "e2e-" + strings.ToLower(time.Now().UTC().Format("150405"))
	owner := provider.Owner{AccountID: "e2e", BoxID: name, Lease: "sevalla-e2e"}
	p := New(Config{
		Token:          token,
		ClusterID:      cluster,
		ResourceTypeID: os.Getenv("VMBOX_SEVALLA_RESOURCE_TYPE_ID"),
		DefaultImage:   envOr("VMBOX_E2E_IMAGE", "nginx:1.27-alpine"),
	})
	box, err := p.Create(ctx, provider.CreateRequest{Name: name, Owner: owner})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 2*time.Minute)
		defer done()
		if err := p.Delete(cleanup, box.ID, owner); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	if box.Owner.AccountID != owner.AccountID || box.Owner.BoxID != owner.BoxID {
		t.Fatalf("ownership mismatch: %+v", box.Owner)
	}
	argv := []string{"printf", "%s", `$HOME; $(touch /tmp/not-run)`, "two words"}
	result, err := p.Exec(ctx, box.ID, argv, provider.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Stdout != argv[2]+argv[3] {
		t.Fatalf("exact argv result: exit=%d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
