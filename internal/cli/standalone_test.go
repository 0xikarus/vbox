package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/procexec"
)

func TestStandaloneCommandsBypassFleetConnection(t *testing.T) {
	for _, command := range [][]string{{"standalone", "help"}, {"standalone", "list"}} {
		a := New()
		var out bytes.Buffer
		a.Out = &out
		a.Err = &out
		a.ConfigPath = filepath.Join(t.TempDir(), "no-fleet.json")
		a.IsTerminal = func() bool { return false }
		if err := a.Run(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "connect URL") {
			t.Fatal(out.String())
		}
	}
}
func TestStandaloneVMIsNeverSilentlyDowngraded(t *testing.T) {
	a := New()
	a.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	a.IsTerminal = func() bool { return false }
	runner := &procexec.FakeRunner{}
	a.Runner = runner
	err := a.Run(context.Background(), []string{"standalone", "create", "testbox", "--no-dialog", "--model-url", "http://model:8080/v1", "--model", "test"})
	if err == nil || !strings.Contains(err.Error(), "VM installation is not implemented") {
		t.Fatal(err)
	}
	if len(runner.Calls) != 0 {
		t.Fatal("engine called before unsupported mode rejected")
	}
}
func TestStandaloneRejectsCredentialsInModelURL(t *testing.T) {
	a := New()
	a.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	a.IsTerminal = func() bool { return false }
	runner := &procexec.FakeRunner{}
	a.Runner = runner
	err := a.Run(context.Background(), []string{"standalone", "create", "testbox", "--mode", "container", "--no-dialog", "--model-url", "http://user:password@model/v1", "--model", "test"})
	if err == nil || len(runner.Calls) != 0 {
		t.Fatalf("err=%v calls=%d", err, len(runner.Calls))
	}
}
