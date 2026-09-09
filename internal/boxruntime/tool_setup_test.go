package boxruntime

import (
	"context"
	"errors"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCustomToolSetupAndRestore(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	workspace := filepath.Join(base, "workspace")
	os.Mkdir(workspace, 0700)
	script := `mkdir -p "$HOME/bin"; printf '%s\n' '#!/bin/sh' 'printf custom-tool-ok' > "$HOME/bin/custom-tool"; chmod 700 "$HOME/bin/custom-tool"; printf 'installed\n' >> setup-count`
	if err := ConfigureToolSetup(context.Background(), home, script, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := RestoreToolSetup(context.Background(), home, io.Discard); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(workspace, "setup-count"))
	if err != nil || string(b) != "installed\ninstalled\n" {
		t.Fatalf("restore did not apply recipe: %q %v", b, err)
	}
	path, _ := toolSetupPath(home)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("recipe permissions", err)
	}
	if err := ConfigureToolSetup(context.Background(), home, "exit 17", io.Discard); err == nil || !strings.Contains(err.Error(), "exit status 17") {
		t.Fatal("setup failure not reported", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := ConfigureToolSetup(ctx, home, "sleep 60", io.Discard); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("missing setup deadline", err)
	}
	if err := ConfigureToolSetup(context.Background(), home, strings.Repeat("x", 32769), io.Discard); err == nil {
		t.Fatal("accepted oversized script")
	}
}

func TestCustomToolFailurePreventsTask(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, ".vmbox")
	os.Mkdir(filepath.Join(base, "workspace"), 0700)
	dir, _ := processDir(root, "setup-failure")
	os.MkdirAll(dir, 0700)
	task := v1.ProcessTask{ID: "setup-failure", Agent: "shell", Prompt: "touch should-not-run", SetupScript: "exit 19"}
	if err := writeProcessJSON(filepath.Join(dir, "task.json"), task); err != nil {
		t.Fatal(err)
	}
	if err := RunProcess(root, task.ID); err != nil {
		t.Fatal(err)
	}
	result, err := ReadProcess(root, task.ID)
	if err != nil || result.State != "setup_failed" || result.ExitCode != nil || result.FinishedAt == nil {
		t.Fatalf("result %+v %v", result, err)
	}
	if _, err = os.Stat(filepath.Join(base, "workspace", "should-not-run")); !os.IsNotExist(err) {
		t.Fatal("task ran after setup failure")
	}
}
