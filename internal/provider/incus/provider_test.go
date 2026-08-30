package incus

import (
	"context"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"reflect"
	"testing"
)

func TestExecPreservesExactArgv(t *testing.T) {
	list := `[{"name":"vmbox-box","status":"Running","config":{"user.vmbox.managed":"true","user.vmbox.account":"a","user.vmbox.box":"box"}}]`
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte(list)}, {}}}
	p := New(Config{Remote: "host"}, runner)
	argv := []string{"echo", "a b", "$x", ";"}
	_, err := p.Exec(context.Background(), "box", argv, provider.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string{"incus", "exec", "host:vmbox-box", "--"}, argv...)
	if !reflect.DeepEqual(runner.Calls[1].Argv, want) {
		t.Fatalf("got %#v want %#v", runner.Calls[1].Argv, want)
	}
}
