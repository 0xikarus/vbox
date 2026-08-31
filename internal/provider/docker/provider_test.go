package docker

import (
	"context"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"reflect"
	"strings"
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

func TestExecKeepsStdinOpenWithoutAllocatingTTY(t *testing.T) {
	runner := &procexec.FakeRunner{}
	p := New(Config{}, runner)
	_, err := p.Exec(context.Background(), "box", []string{"vmbox-runtime", "put-file", "/data/file", "0600"}, provider.ExecOptions{Stdin: strings.NewReader("secret")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docker", "container", "exec", "--interactive", "vmbox-box", "vmbox-runtime", "put-file", "/data/file", "0600"}
	if !reflect.DeepEqual(runner.Calls[0].Argv, want) {
		t.Fatalf("argv=%#v want=%#v", runner.Calls[0].Argv, want)
	}
}
func TestDetachedExecUsesGenericRuntime(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{ExitCode: 0}}}
	p := New(Config{}, runner)
	argv := []string{"tool", "--flag", "two words"}
	_, err := p.Exec(context.Background(), "box", argv, provider.ExecOptions{Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string{"docker", "container", "exec", "vmbox-box", "vmbox-runtime", "run", "--detach", "--"}, argv...)
	if got := runner.Calls[0].Argv; !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestInspectTreatsExitedAsStoppedAndRestoresDiskSpec(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte(`[{"Id":"container-id","Created":"2026-08-30T00:00:00Z","Config":{"Image":"node:22-bookworm-slim","Env":["VMBOX_DISK_GIB=10"],"Labels":{"dev.vmbox.account":"standalone","dev.vmbox.box":"box"}},"State":{"Status":"exited","Running":false},"HostConfig":{"NanoCpus":2000000000,"Memory":4294967296},"Image":"sha256:image"}]`)}}}
	p := New(Config{}, runner)
	box, err := p.Inspect(context.Background(), "box")
	if err != nil {
		t.Fatal(err)
	}
	if box.State != provider.StateStopped {
		t.Fatalf("state=%s", box.State)
	}
	if box.Resources.DiskGiB != 10 || box.Storage == nil || box.Storage.SizeGiB != 10 {
		t.Fatalf("resources=%+v storage=%+v", box.Resources, box.Storage)
	}
}

func TestRejectsUnauthenticatedTCP(t *testing.T) {
	p := New(Config{Host: "tcp://example:2375"}, &procexec.FakeRunner{})
	if _, err := p.Validate(context.Background()); err == nil {
		t.Fatal("unsafe Docker endpoint accepted")
	}
}
