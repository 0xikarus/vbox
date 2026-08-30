package boxruntime

import (
	"context"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"os"
	"path/filepath"
	"testing"
)

func TestRunExactArgvAndDurableStatus(t *testing.T) {
	root := t.TempDir()
	r := New(root)
	r.Heartbeat = 10
	run, err := r.Run(context.Background(), []string{"printf", "%s", `$HOME; $(false)`}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != v1.JobSucceeded || run.LastActivity != `$HOME; $(false)` {
		t.Fatalf("unexpected run: %+v", run)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", run.ID, "status.json")); err != nil {
		t.Fatal(err)
	}
}
