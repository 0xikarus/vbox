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
	prefix := append([]string{"railway", "ssh", "--project", "project", "--environment", "environment", "--service", "vmbox-box"}, provider.AsWorkloadUser([]string{"vmbox-runtime", "exec-json"})...)
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

func TestAttachSessionUsesRailwayNativeSessionWithoutRemoteCommand(t *testing.T) {
	services := `[{"id":"service-id","name":"vmbox-box","status":"SUCCESS"}]`
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte(services)}, {ExitCode: 1}, {}, {}, {}}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment"}, runner)
	result, err := p.AttachSession(context.Background(), "box", "vmbox", []string{"claude", "task with spaces"}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	got := runner.Calls[4].Argv
	want := []string{"railway", "ssh", "--project", "project", "--environment", "environment", "--service", "vmbox-box", "--session", "vmbox"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session argv=%#v", got)
	}
	if strings.Contains(strings.Join(got, " "), "exec-json") {
		t.Fatalf("native session unexpectedly supplied a remote command: %#v", got)
	}
	created := runner.Calls[2].Argv
	if !strings.Contains(strings.Join(created, " "), "direct-json") || !strings.Contains(strings.Join(created, " "), "sudo -n -H -u vmbox") {
		t.Fatalf("tmux pane was not created as vmbox: %#v", created)
	}
}

func TestIdleSessionRecognizesOnlyShellPanes(t *testing.T) {
	if !idleSession("bash\nsh\n") || idleSession("bash\nclaude\n") || idleSession("") {
		t.Fatal("idle session classification is unsafe")
	}
}

func TestExecRetriesRotatedHostKeyOnlyInIsolatedFile(t *testing.T) {
	services := `[{"id":"service-id","name":"vmbox-box","status":"SUCCESS"}]`
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(services)},
		{ExitCode: 255, Stderr: []byte("WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!")},
		{},
		{Stdout: []byte("ok")},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", SSHKnownHostsFile: "/isolated/railway-known-hosts"}, runner)
	result, err := p.Exec(context.Background(), "box", []string{"printf", "ok"}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 || result.Stdout != "ok" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	want := []string{"ssh-keygen", "-f", "/isolated/railway-known-hosts", "-R", "ssh.railway.com"}
	if got := runner.Calls[2].Argv; !reflect.DeepEqual(got, want) {
		t.Fatalf("host-key cleanup=%#v want=%#v", got, want)
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

func TestDeploymentSubmissionPollsUntilRecordBecomesVisible(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(`[]`)},
		{Stdout: []byte(`{}`)},
		{Stdout: []byte(`[]`)},
		{Stdout: []byte(`[{"id":"deployment-new","status":"BUILDING"}]`)},
		{Stdout: []byte(`[{"id":"deployment-new","status":"SUCCESS"}]`)},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", PollInterval: time.Millisecond, ReadyTimeout: time.Second}, runner)
	if err := p.submitAndWaitDeployment(context.Background(), "vmbox-box"); err != nil {
		t.Fatal(err)
	}
	if len(runner.Calls) != 5 {
		t.Fatalf("calls=%#v", runner.Calls)
	}
}

func TestEmptyDeploymentStatusIsStopped(t *testing.T) {
	if got := state(""); got != provider.StateStopped {
		t.Fatalf("state(empty) = %q, want %q", got, provider.StateStopped)
	}
}

func TestSetRegionUsesMultiRegionConfigAndClearsDefaults(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte(`{"data":{"serviceInstanceUpdate":true}}`)}}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment"}, runner)
	if err := p.setRegion(context.Background(), "service", "ams"); err != nil {
		t.Fatal(err)
	}
	call := runner.Calls[0].Argv
	variables := ""
	for i, value := range call {
		if value == "--variables" && i+1 < len(call) {
			variables = call[i+1]
		}
	}
	if !strings.Contains(variables, `"multiRegionConfig"`) || !strings.Contains(variables, `"ams":{"numReplicas":1}`) || !strings.Contains(variables, `"iad":null`) {
		t.Fatalf("variables=%s", variables)
	}
}

func TestResourcesMatchOnlyChecksRequestedLimits(t *testing.T) {
	actual := provider.Resources{CPU: 2, MemoryMiB: 1024, DiskGiB: 20}
	if !resourcesMatch(actual, provider.Resources{CPU: 2, MemoryMiB: 1024}) {
		t.Fatal("matching requested limits were rejected")
	}
	if resourcesMatch(actual, provider.Resources{CPU: 4}) {
		t.Fatal("mismatched requested CPU was accepted")
	}
	if resourcesMatch(actual, provider.Resources{MemoryMiB: 2048}) {
		t.Fatal("mismatched requested memory was accepted")
	}
}

func TestCreateWaitsForVolumeThenExactDeployment(t *testing.T) {
	services := `[{"id":"service-id","name":"vmbox-box","status":"SUCCESS"}]`
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(`[]`)},
		{Stdout: []byte(`{"id":"service-id"}`)},
		{Stdout: []byte(services)},
		{},
		{},
		{},
		{},
		{},
		{},
		{Stdout: []byte(services)},
		{Stdout: []byte(`{"volumes":[]}`)},
		{Stdout: []byte(`{"id":"volume-id"}`)},
		{Stdout: []byte(`{"volumes":[{"id":"volume-id","serviceName":"vmbox-box","mountPath":"/data","status":"READY"}]}`)},
		{},
		{},
		{},
		{Stdout: []byte(`[]`)},
		{Stdout: []byte(`{"id":"deployment-new"}`)},
		{Stdout: []byte(`[{"id":"deployment-new","status":"SUCCESS"},{"id":"deployment-other","status":"FAILED"}]`)},
		{Stdout: []byte(services)},
		{Stdout: []byte(`{"VMBOX_ACCOUNT_ID":"standalone","VMBOX_BOX_ID":"box","VMBOX_CPU":"2","VMBOX_MEMORY_MIB":"4096","VMBOX_DISK_GIB":"10"}`)},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", PollInterval: time.Millisecond, ReadyTimeout: time.Second}, runner)
	box, err := p.Create(context.Background(), provider.CreateRequest{Name: "box", Owner: provider.Owner{AccountID: "standalone", BoxID: "box"}, Resources: provider.Resources{CPU: 2, MemoryMiB: 4096, DiskGiB: 10}})
	if err != nil {
		t.Fatalf("create: %v calls=%#v", err, runner.Calls)
	}
	if box.Name != "box" {
		t.Fatalf("box=%+v", box)
	}
	if box.Resources.CPU != 2 || box.Resources.MemoryMiB != 4096 || box.Resources.DiskGiB != 10 || box.Storage.SizeGiB != 10 {
		t.Fatalf("reconstructed specs=%+v storage=%+v", box.Resources, box.Storage)
	}
	volume, deploy := -1, -1
	for i, call := range runner.Calls {
		joined := strings.Join(call.Argv, " ")
		if strings.Contains(joined, " volume ") && strings.Contains(joined, " add ") {
			volume = i
		}
		if strings.Contains(joined, " redeploy ") {
			deploy = i
			if !strings.Contains(joined, " --from-source") {
				t.Fatalf("Railway deployment must be restartable after down: %#v", call.Argv)
			}
		}
	}
	for _, call := range runner.Calls {
		if strings.Contains(strings.Join(call.Argv, " "), " variable set ") {
			if call.Stdin == "" {
				t.Fatal("Railway variable write did not use stdin")
			}
			for _, arg := range call.Argv {
				if arg == call.Stdin {
					t.Fatalf("variable value leaked into argv: %#v", call)
				}
			}
		}
	}
	if volume < 0 || deploy < 0 || volume >= deploy {
		t.Fatalf("volume=%d deploy=%d calls=%#v", volume, deploy, runner.Calls)
	}
}

func TestStopPowersDownDeploymentAndStartRedeploysService(t *testing.T) {
	running := `[{"id":"service-id","name":"vmbox-box","status":"SUCCESS"}]`
	stopped := `[{"id":"service-id","name":"vmbox-box","status":"NO_DEPLOYMENT"}]`
	variables := `{"VMBOX_ACCOUNT_ID":"standalone","VMBOX_BOX_ID":"box","VMBOX_CPU":"2","VMBOX_MEMORY_MIB":"4096","VMBOX_DISK_GIB":"10"}`
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(running)},
		{},
		{Stdout: []byte(stopped)},
		{Stdout: []byte(variables)},
		{},
		{Stdout: []byte(stopped)},
		{Stdout: []byte(`[]`)},
		{Stdout: []byte(`{"id":"deployment-new"}`)},
		{Stdout: []byte(`[{"id":"deployment-new","status":"SUCCESS"}]`)},
		{Stdout: []byte(running)},
		{Stdout: []byte(variables)},
		{},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", PollInterval: time.Millisecond, ReadyTimeout: time.Second}, runner)

	box, err := p.Stop(context.Background(), "box")
	if err != nil {
		t.Fatal(err)
	}
	if box.State != provider.StateStopped || box.ProviderState != "NO_DEPLOYMENT" {
		t.Fatalf("stopped box=%+v", box)
	}

	box, err = p.Start(context.Background(), "box")
	if err != nil {
		t.Fatal(err)
	}
	if box.State != provider.StateRunning {
		t.Fatalf("resumed box=%+v", box)
	}

	commands := make([]string, 0, len(runner.Calls))
	for _, call := range runner.Calls {
		command := strings.Join(call.Argv, " ")
		commands = append(commands, command)
		if strings.Contains(command, " service delete ") || strings.Contains(command, " volume delete ") {
			t.Fatalf("power lifecycle deleted persistent resources: %s", command)
		}
	}
	joined := strings.Join(commands, "\n")
	if !strings.Contains(joined, "railway down --service vmbox-box --yes") {
		t.Fatalf("stop did not remove the active deployment:\n%s", joined)
	}
	if !strings.Contains(joined, "railway redeploy --service vmbox-box --yes --json --from-source") {
		t.Fatalf("start did not redeploy the preserved service:\n%s", joined)
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
