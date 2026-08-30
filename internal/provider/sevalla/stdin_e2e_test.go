package sevalla

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

// TestSevallaExistingRuntimeStdinE2E verifies the terminal channel against an
// already-running vmbox runtime. It never creates or deletes an application.
func TestSevallaExistingRuntimeStdinE2E(t *testing.T) {
	name := os.Getenv("VMBOX_E2E_SEVALLA_EXISTING")
	if name == "" {
		t.Skip("set VMBOX_E2E_SEVALLA_EXISTING to a running vmbox application")
	}
	token := os.Getenv("SEVALLA_API_TOKEN")
	if token == "" {
		t.Fatal("SEVALLA_API_TOKEN is required")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	secret := "stdin-e2e-" + hex.EncodeToString(random)
	path := "/data/home/.vmbox-stdin-e2e"
	p := New(Config{Token: token})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		_, _ = p.Exec(cleanup, name, []string{"rm", "-f", path}, provider.ExecOptions{})
	})
	var terminal bytes.Buffer
	result, err := p.Exec(ctx, name, []string{"sh", "-c", `cat >"$1"`, "vmbox-stdin", path}, provider.ExecOptions{Stdin: strings.NewReader(secret), Stdout: &terminal})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("put-file exit status = %d terminal=%q", result.ExitCode, terminal.String())
	}
	if strings.Contains(terminal.String(), secret) {
		t.Fatal("terminal echoed secret stdin")
	}
	verified, err := p.Exec(ctx, name, []string{"cat", path}, provider.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if verified.ExitCode != 0 || verified.Stdout != secret {
		t.Fatalf("uploaded content mismatch: exit=%d stdout=%q stderr=%q", verified.ExitCode, verified.Stdout, verified.Stderr)
	}
}
