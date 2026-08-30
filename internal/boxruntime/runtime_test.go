package boxruntime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestRunExactArgvAndDurableStatus(t *testing.T) {
	root := t.TempDir()
	r := New(root)
	var stdout bytes.Buffer
	run, err := r.Run(context.Background(), []string{"printf", "%s", `$HOME; $(false)`}, &stdout, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != v1.JobSucceeded || stdout.String() != `$HOME; $(false)` {
		t.Fatalf("unexpected run: %+v", run)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", run.ID, "status.json")); err != nil {
		t.Fatal(err)
	}
}

func TestDetachedControllerRunIDIsStableAndIdempotent(t *testing.T) {
	root := t.TempDir()
	runtime := New(root)
	t.Setenv("VMBOX_RUN_ID", "controller-run-1")
	store, err := runtime.StoreFor("controller-run-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStatus(v1.Run{ID: "controller-run-1", State: v1.JobRunning, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	id, err := runtime.RunDetached([]string{"this-command-must-not-run"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "controller-run-1" {
		t.Fatalf("run ID=%q", id)
	}
}
