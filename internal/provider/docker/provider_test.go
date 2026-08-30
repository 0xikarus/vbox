package docker

import (
	"context"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"reflect"
	"testing"
)

func TestExecPreservesExactArgv(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{ExitCode: 0}}}
	p := New(Config{Context: "ssh-builder"}, runner)
	argv := []string{"printf", "%s", `$HOME; $(touch nope)`, "space value"}
	_, err := p.Exec(context.Background(), "box", argv, provider.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := runner.Calls[0].Argv
	want := append([]string{"docker", "--context", "ssh-builder", "container", "exec", "vmbox-box"}, argv...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch\n got: %#v\nwant: %#v", got, want)
	}
}
func TestRejectsUnauthenticatedTCP(t *testing.T) {
	p := New(Config{Host: "tcp://example:2375"}, &procexec.FakeRunner{})
	if _, err := p.Validate(context.Background()); err == nil {
		t.Fatal("unsafe Docker endpoint accepted")
	}
}
