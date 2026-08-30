package railway

import (
	"context"
	"errors"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestConfiguredTokenIsOnlyInProcessEnvironment(t *testing.T) {
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", Token: "configured-secret"}, procexec.OSRunner{})
	runner, ok := p.runner.(procexec.OSRunner)
	if !ok || runner.Env["RAILWAY_API_TOKEN"] != "configured-secret" {
		t.Fatalf("runner=%T env=%v", p.runner, runner.Env)
	}
	for _, arg := range p.command("service", "list", "--json") {
		if strings.Contains(arg, "configured-secret") {
			t.Fatalf("token leaked into argv: %#v", p.command("service", "list", "--json"))
		}
	}
}

func TestDeploymentIDPrefersSubmittedDeploymentOverService(t *testing.T) {
	data := []byte(`{"service":{"id":"service-id"},"deployment":{"id":"deployment-id"}}`)
	if got := deploymentID(data); got != "deployment-id" {
		t.Fatalf("deployment ID=%q", got)
	}
}

func TestCreateWaitsForVolumeThenExactDeployment(t *testing.T) {
	services := `[{"id":"service-id","name":"vmbox-box","status":"SUCCESS"}]`
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(`[]`)},
		{Stdout: []byte(`{"id":"service-id"}`)},
		{Stdout: []byte(services)},
		{},
		{Stdout: []byte(services)},
		{Stdout: []byte(`{"volumes":[]}`)},
		{Stdout: []byte(`{"id":"volume-id"}`)},
		{Stdout: []byte(`{"volumes":[{"id":"volume-id","serviceName":"vmbox-box","mountPath":"/data","status":"READY"}]}`)},
		{},
		{Stdout: []byte(`[]`)},
		{Stdout: []byte(`{"id":"deployment-new"}`)},
		{Stdout: []byte(`[{"id":"deployment-new","status":"SUCCESS"},{"id":"deployment-other","status":"FAILED"}]`)},
		{Stdout: []byte(services)},
		{Stdout: []byte(`{"VMBOX_ACCOUNT_ID":"standalone","VMBOX_BOX_ID":"box"}`)},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", PollInterval: time.Millisecond, ReadyTimeout: time.Second}, runner)
	box, err := p.Create(context.Background(), provider.CreateRequest{Name: "box", Owner: provider.Owner{AccountID: "standalone", BoxID: "box"}, Resources: provider.Resources{DiskGiB: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if box.Name != "box" {
		t.Fatalf("box=%+v", box)
	}
	volume, deploy := -1, -1
	for i, call := range runner.Calls {
		joined := strings.Join(call.Argv, " ")
		if strings.Contains(joined, "volume add") {
			volume = i
		}
		if strings.Contains(joined, " redeploy ") {
			deploy = i
		}
	}
	if volume < 0 || deploy < 0 || volume >= deploy {
		t.Fatalf("volume=%d deploy=%d calls=%#v", volume, deploy, runner.Calls)
	}
}

func TestInterruptedSubmissionReconcilesExactNewDeployment(t *testing.T) {
	services := `[{"id":"service-id","name":"vmbox-box","status":"SUCCESS"}]`
	runner := &procexec.FakeRunner{
		Results: []procexec.Result{
			{Stdout: []byte(services)},
			{Stdout: []byte(`[{"id":"old","status":"SUCCESS"}]`)},
			{ExitCode: 1, Stderr: []byte("connection reset")},
			{Stdout: []byte(`[{"id":"new","status":"BUILDING"},{"id":"old","status":"SUCCESS"}]`)},
			{Stdout: []byte(`[{"id":"old","status":"SUCCESS"},{"id":"new","status":"SUCCESS"}]`)},
			{Stdout: []byte(services)},
			{Stdout: []byte(`{"VMBOX_ACCOUNT_ID":"standalone","VMBOX_BOX_ID":"box"}`)},
		},
		Errors: []error{nil, nil, errors.New("transport interrupted")},
	}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", PollInterval: time.Millisecond, ReadyTimeout: time.Second}, runner)
	if _, err := p.Deploy(context.Background(), "box", ""); err != nil {
		t.Fatal(err)
	}
}
