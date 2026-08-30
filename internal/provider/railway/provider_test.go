package railway

import (
	"context"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"reflect"
	"testing"
)

func TestExecEncodesExactArgvWithoutShell(t *testing.T) {
	services := `[{"id":"service-id","name":"vmbox-box","status":"SUCCESS"}]`
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte(services)}, {}}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment"}, runner)
	argv := []string{"printf", "%s", `$HOME; $(touch nope)`, "two words"}
	_, err := p.Exec(context.Background(), "box", argv, provider.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := runner.Calls[1].Argv
	prefix := []string{"railway", "ssh", "--project", "project", "--environment", "environment", "--service", "vmbox-box", "vmbox-runtime", "exec-json"}
	if len(got) != len(prefix)+1 || !reflect.DeepEqual(got[:len(prefix)], prefix) {
		t.Fatalf("unexpected transport argv: %#v", got)
	}
	decoded, err := boxruntime.DecodeArgv(got[len(prefix)])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, argv) {
		t.Fatalf("decoded=%#v want=%#v", decoded, argv)
	}
}
