package cli

import (
	"bytes"
	"context"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneMissingDefaultImageBuildsOnSelectedHost(t *testing.T) {
	a := New()
	a.Err = &bytes.Buffer{}
	a.WorkingDir = t.TempDir()
	for _, name := range []string{"Dockerfile.standalone", "go.mod", ".dockerignore"} {
		os.WriteFile(filepath.Join(a.WorkingDir, name), []byte("fixture"), 0600)
	}
	r := &procexec.FakeRunner{Results: []procexec.Result{{ExitCode: 1}, {}}}
	a.Runner = r
	if err := a.prepareStandaloneImage(context.Background(), "pgx@pgx.local", "vbox-standalone:dev"); err != nil {
		t.Fatal(err)
	}
	if len(r.Calls) != 2 {
		t.Fatal(len(r.Calls))
	}
	args := strings.Join(r.Calls[1].Argv, " ")
	if !strings.Contains(args, "docker --host ssh://pgx@pgx.local build --file ") || !strings.Contains(args, "--tag vbox-standalone:dev") {
		t.Fatal(args)
	}
}
func TestStandaloneMissingDefaultImageOutsideRepoIsActionable(t *testing.T) {
	a := New()
	a.WorkingDir = t.TempDir()
	r := &procexec.FakeRunner{Results: []procexec.Result{{ExitCode: 1}}}
	a.Runner = r
	if err := a.prepareStandaloneImage(context.Background(), "remote", "vbox-standalone:dev"); err == nil || !strings.Contains(err.Error(), "install.sh --standalone") {
		t.Fatal(err)
	}
	if len(r.Calls) != 1 {
		t.Fatal("attempted registry pull or resource creation")
	}
}
func TestStandaloneWizardCannotSwitchContainerBackToVM(t *testing.T) {
	a := New()
	a.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	a.Err = &bytes.Buffer{}
	a.Out = &bytes.Buffer{}
	a.IsTerminal = func() bool { return true }
	t.Setenv("HOME", t.TempDir())
	// Select Isolation and press Enter; then submit the form.
	a.In = strings.NewReader("\t\t\t\r\x1b[A\x1b[A\x1b[A\x1b[A\r")
	r := &procexec.FakeRunner{Results: []procexec.Result{{}, {}, {}, {ExitCode: 1}}}
	a.Runner = r
	a.WorkingDir = t.TempDir()
	err := a.Run(context.Background(), []string{"standalone", "create", "my-box", "--host", "pgx@pgx.local", "--mode", "container", "--listen", "0.0.0.0", "--port", "8088", "--model-url", "http://pgx.local:8000/v1", "--model", "test"})
	if err == nil || strings.Contains(err.Error(), "VM installation") {
		t.Fatalf("container flag lost by wizard: %v", err)
	}
	// Capacity checks and image preparation are read-only; no instance gets registered.
	if _, e := os.Stat(filepath.Join(filepath.Dir(a.ConfigPath), "standalone", "my-box.json")); !os.IsNotExist(e) {
		t.Fatal("failed preparation left an instance record")
	}
}
