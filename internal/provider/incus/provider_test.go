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
	want := append([]string{"incus", "exec", "host:vmbox-box", "--"}, provider.AsWorkloadUser(argv)...)
	if !reflect.DeepEqual(runner.Calls[1].Argv, want) {
		t.Fatalf("got %#v want %#v", runner.Calls[1].Argv, want)
	}
}

func TestBoxRestoresImageRegionAndDiskSpecs(t *testing.T) {
	p := New(Config{Remote: "ubuntu-host"}, &procexec.FakeRunner{})
	box := p.toBox(instance{Name: "vmbox-worker", Status: "Running", Config: map[string]string{
		"limits.cpu": "2", "limits.memory": "4096MiB", "user.vmbox.disk-gib": "20", "user.vmbox.image": "images:ubuntu/24.04",
	}})
	if box.Image != "images:ubuntu/24.04" || box.Region != "ubuntu-host" || box.Resources.DiskGiB != 20 || box.Storage == nil || box.Storage.SizeGiB != 20 {
		t.Fatalf("box=%+v storage=%+v", box, box.Storage)
	}
}

func TestDeleteStorageIsIdempotentWhenVolumeIsAbsent(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{ExitCode: 1, Stderr: []byte("storage volume not found")}}}
	p := New(Config{}, runner)
	if err := p.DeleteStorage(context.Background(), provider.Storage{Name: "vmbox-worker-data"}, provider.Owner{AccountID: "account", BoxID: "worker"}); err != nil {
		t.Fatal(err)
	}
}
