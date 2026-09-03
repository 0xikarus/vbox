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

func TestRunRecordsExecutableStartupFailure(t *testing.T) {
	runtime := New(t.TempDir())
	run, err := runtime.Run(context.Background(), []string{"this-vmbox-command-does-not-exist"}, nil, nil)
	if err == nil {
		t.Fatal("expected startup error")
	}
	if run.State != v1.JobFailed || run.ExitCode == nil || *run.ExitCode != 127 || run.FinishedAt == nil {
		t.Fatalf("run=%+v", run)
	}
	stored, readErr := runtime.StoreFor(run.ID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	status, readErr := stored.ReadStatus()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if status.State != v1.JobFailed || status.ExitCode == nil || *status.ExitCode != 127 {
		t.Fatalf("stored status=%+v", status)
	}
}

func TestRuntimeEnvironmentReplacesInheritedEmptyValues(t *testing.T) {
	t.Setenv("VMBOX_RUN_ID", "")
	t.Setenv("VMBOX_RUNTIME_DIR", "old")
	values := runtimeEnvironment("run-new", "/data/.vmbox")
	foundRun, foundRoot := 0, 0
	for _, value := range values {
		switch value {
		case "VMBOX_RUN_ID=run-new":
			foundRun++
		case "VMBOX_RUNTIME_DIR=/data/.vmbox":
			foundRoot++
		}
		if value == "VMBOX_RUN_ID=" || value == "VMBOX_RUNTIME_DIR=old" {
			t.Fatalf("stale runtime environment retained: %q", value)
		}
	}
	if foundRun != 1 || foundRoot != 1 {
		t.Fatalf("run entries=%d root entries=%d", foundRun, foundRoot)
	}
}
