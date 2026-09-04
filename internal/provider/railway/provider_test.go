package railway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestExecUsesDirectSSHAndEncodesExactArgv(t *testing.T) {
	services := `[{"id":"service-id","name":"vmbox-box","status":"SUCCESS"}]`
	instance := `{"data":{"serviceInstance":{"id":"deployment-instance"}}}`
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(services)}, {Stdout: []byte(instance)}, {},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment"}, runner)
	argv := []string{"printf", "%s", `$HOME; $(touch nope)`, "two words"}
	_, err := p.Exec(context.Background(), "box", argv, provider.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := runner.Calls[2].Argv
	if got[0] != "ssh" || got[len(got)-2] != "deployment-instance@ssh.railway.com" {
		t.Fatalf("unexpected direct SSH argv: %#v", got)
	}
	encoded, _ := json.Marshal(argv)
	payload := base64.RawURLEncoding.EncodeToString(encoded)
	command := got[len(got)-1]
	if !strings.Contains(command, "'vmbox-runtime' 'exec-json' '"+payload+"'") {
		t.Fatalf("exact argv payload missing from remote command: %s", command)
	}
	if strings.Contains(command, "touch nope") {
		t.Fatalf("untrusted argv appeared as shell source: %s", command)
	}
	for _, call := range runner.Calls {
		if len(call.Argv) > 1 && call.Argv[0] == "railway" && call.Argv[1] == "ssh" {
			t.Fatalf("runtime data path invoked railway ssh: %#v", call.Argv)
		}
	}
}

func TestAttachSessionUsesDirectInteractiveSSH(t *testing.T) {
	services := `[{"id":"service-id","name":"vmbox-box","status":"SUCCESS"}]`
	instance := `{"data":{"serviceInstance":{"id":"deployment-instance"}}}`
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(services)}, {Stdout: []byte(instance)}, {Stdout: []byte("created\n")}, {},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment"}, runner)
	result, err := p.AttachSession(context.Background(), "box", "vmbox", []string{"claude", "task with spaces"}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	got := runner.Calls[3].Argv
	joined := strings.Join(got, " ")
	if got[0] != "ssh" || !strings.Contains(joined, " -tt ") || got[len(got)-2] != "deployment-instance@ssh.railway.com" {
		t.Fatalf("interactive direct SSH argv=%#v", got)
	}
	if !strings.Contains(got[len(got)-1], "'tmux' 'attach-session' '-t' 'vmbox'") || !strings.Contains(got[len(got)-1], "'sudo' '-n' '-H' '-u' 'vmbox'") {
		t.Fatalf("session did not attach to the workload user's tmux server: %s", got[len(got)-1])
	}
	created := runner.Calls[2].Argv
	createdCommand := created[len(created)-1]
	if !strings.Contains(createdCommand, "direct-json") || !strings.Contains(createdCommand, "tmux has-session") {
		t.Fatalf("tmux preparation was not one direct batched command: %#v", created)
	}
	if len(runner.Calls) != 4 {
		t.Fatalf("unexpected attach call count: %#v", runner.Calls)
	}
}

func TestAttachConnectionUsesValidatedTargetWithoutRailwayLookup(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte("created\n")}, {}}}
	p := New(Config{}, runner)
	connection := provider.Connection{Transport: "openssh", Endpoint: "deployment-controller@ssh.railway.com", Metadata: map[string]string{"deploymentInstanceId": "deployment-controller"}}
	result, err := p.AttachConnection(context.Background(), connection, "vmbox", []string{"vmbox-runtime", "welcome"}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if len(runner.Calls) != 2 {
		t.Fatalf("calls=%#v", runner.Calls)
	}
	for _, call := range runner.Calls {
		if call.Argv[0] != "ssh" || strings.Contains(strings.Join(call.Argv, " "), "railway ssh") {
			t.Fatalf("controller attach used control-plane lookup: %#v", call.Argv)
		}
		if call.Argv[len(call.Argv)-2] != connection.Endpoint {
			t.Fatalf("SSH target=%#v", call.Argv)
		}
	}
	prepared := runner.Calls[0].Argv[len(runner.Calls[0].Argv)-1]
	if !strings.HasPrefix(prepared, "'sudo' '-n' '-H' '-u' 'vmbox'") || !strings.Contains(prepared, "'sh' '-c'") {
		t.Fatalf("tmux preparation did not run entirely as workload user: %s", prepared)
	}
}

func TestAttachConnectionRejectsUntrustedEndpointBeforeExecution(t *testing.T) {
	for _, connection := range []provider.Connection{
		{Transport: "railway-cli", Endpoint: "deployment@ssh.railway.com", Metadata: map[string]string{"deploymentInstanceId": "deployment"}},
		{Transport: "openssh", Endpoint: "deployment@evil.example", Metadata: map[string]string{"deploymentInstanceId": "deployment"}},
		{Transport: "openssh", Endpoint: "other@ssh.railway.com", Metadata: map[string]string{"deploymentInstanceId": "deployment"}},
		{Transport: "openssh", Endpoint: "bad/instance@ssh.railway.com", Metadata: map[string]string{"deploymentInstanceId": "bad/instance"}},
	} {
		runner := &procexec.FakeRunner{}
		p := New(Config{}, runner)
		if _, err := p.AttachConnection(context.Background(), connection, "vmbox", []string{"bash"}, provider.ExecOptions{}); err == nil {
			t.Fatalf("accepted connection=%+v", connection)
		}
		if len(runner.Calls) != 0 {
			t.Fatalf("executed untrusted connection: %#v", runner.Calls)
		}
	}
}

func TestIdleSessionRecognizesOnlyShellPanes(t *testing.T) {
	if !idleSession("bash\nsh\n") || idleSession("bash\nclaude\n") || idleSession("") {
		t.Fatal("idle session classification is unsafe")
	}
}

func TestExecRepairsRotatedHostKeyBeforeStartingMaster(t *testing.T) {
	services := `[{"id":"service-id","name":"vmbox-box","status":"SUCCESS"}]`
	instance := `{"data":{"serviceInstance":{"id":"deployment-instance"}}}`
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	controlDir := filepath.Join(dir, "control")
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(services)},
		{Stdout: []byte(instance)},
		{ExitCode: 255, Stderr: []byte("WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!")},
		{},
		{ExitCode: 255, Stderr: []byte("Control socket does not exist")},
		{},
		{Stdout: []byte("ok")},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", SSHKnownHostsFile: knownHosts, SSHControlDir: controlDir}, runner)
	result, err := p.Exec(context.Background(), "box", []string{"printf", "ok"}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 || result.Stdout != "ok" {
		t.Fatalf("result=%+v err=%v calls=%#v", result, err, runner.Calls)
	}
	want := []string{"ssh-keygen", "-f", knownHosts, "-R", "ssh.railway.com"}
	if got := runner.Calls[3].Argv; !reflect.DeepEqual(got, want) {
		t.Fatalf("host-key cleanup=%#v want=%#v", got, want)
	}
	master := strings.Join(runner.Calls[5].Argv, " ")
	if !strings.Contains(master, " -M -f ") || strings.Contains(master, " -N ") || !strings.Contains(master, "ControlMaster=yes") || !strings.Contains(master, "ControlPersist=120") || !strings.HasSuffix(master, " "+railwaySSHMasterKeepalive) {
		t.Fatalf("explicit detached master was not started: %s", master)
	}
}

func TestDirectSSHReusesDeploymentLookupAndControlMaster(t *testing.T) {
	services := `[{"id":"service-id","name":"vmbox-box","status":"SUCCESS"}]`
	instance := `{"data":{"serviceInstance":{"id":"deployment-instance"}}}`
	dir := t.TempDir()
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(services)},
		{Stdout: []byte(instance)},
		{ExitCode: 255},
		{},
		{Stdout: []byte("one")},
		{Stdout: []byte("two")},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", SSHControlDir: filepath.Join(dir, "control")}, runner)
	if _, err := p.Exec(context.Background(), "box", []string{"printf", "one"}, provider.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(context.Background(), "box", []string{"printf", "two"}, provider.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	apiCalls, masterStarts, dataCalls := 0, 0, 0
	for _, call := range runner.Calls {
		joined := strings.Join(call.Argv, " ")
		if len(call.Argv) > 1 && call.Argv[0] == "railway" && call.Argv[1] == "api" && strings.Contains(joined, "serviceInstance") {
			apiCalls++
		}
		if strings.Contains(joined, "ControlMaster=yes") {
			masterStarts++
		}
		if len(call.Argv) > 0 && call.Argv[0] == "ssh" && strings.Contains(joined, "ControlMaster=no") {
			dataCalls++
		}
	}
	if apiCalls != 1 || masterStarts != 1 || dataCalls != 2 {
		t.Fatalf("api=%d masters=%d data=%d calls=%#v", apiCalls, masterStarts, dataCalls, runner.Calls)
	}
}

func TestDirectSSHInvalidatesDeploymentAndRetriesTransportFailure(t *testing.T) {
	services := `[{"id":"service-id","name":"vmbox-box","status":"SUCCESS"}]`
	oldInstance := `{"data":{"serviceInstance":{"id":"deployment-old"}}}`
	newInstance := `{"data":{"serviceInstance":{"id":"deployment-new"}}}`
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(services)},
		{Stdout: []byte(oldInstance)},
		{ExitCode: 255},
		{Stdout: []byte(newInstance)},
		{Stdout: []byte("ok\n")},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment"}, runner)
	result, err := p.Exec(context.Background(), "box", []string{"vmbox-runtime", "health"}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 || result.Stdout != "ok\n" {
		t.Fatalf("result=%+v err=%v calls=%#v", result, err, runner.Calls)
	}
	if len(runner.Calls) != 5 {
		t.Fatalf("calls=%#v", runner.Calls)
	}
	if got := runner.Calls[2].Argv[len(runner.Calls[2].Argv)-2]; got != "deployment-old@ssh.railway.com" {
		t.Fatalf("first target=%q", got)
	}
	if got := runner.Calls[4].Argv[len(runner.Calls[4].Argv)-2]; got != "deployment-new@ssh.railway.com" {
		t.Fatalf("retry target=%q", got)
	}
}

func TestDirectSSHRemovesPoisonedControlSocketOnTransportFailure(t *testing.T) {
	services := `[{"id":"service-id","name":"vmbox-box","status":"SUCCESS"}]`
	instance := `{"data":{"serviceInstance":{"id":"deployment-instance"}}}`
	controlDir := t.TempDir()
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(services)},
		{Stdout: []byte(instance)},
		{},
		{ExitCode: 255, Stderr: []byte("exec request failed")},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", SSHControlDir: controlDir}, runner)
	controlPath, err := p.controlPath("deployment-instance@ssh.railway.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(controlPath), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", controlPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	result, err := p.Exec(context.Background(), "box", []string{"vmbox-runtime", "put-file", "/data/file", "0600"}, provider.ExecOptions{Stdin: strings.NewReader("payload")})
	if err != nil || result.ExitCode != 255 {
		t.Fatalf("result=%+v err=%v calls=%#v", result, err, runner.Calls)
	}
	if _, err := os.Lstat(controlPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("poisoned socket still exists: %v", err)
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

func TestSSHOptionsUseOnlyConfiguredIdentity(t *testing.T) {
	p := New(Config{SSHIdentityFile: "/run/secrets/controller-ssh"}, &procexec.FakeRunner{})
	got := strings.Join(p.sshOptions(""), " ")
	if !strings.Contains(got, "-o IdentitiesOnly=yes -i /run/secrets/controller-ssh") {
		t.Fatalf("SSH options do not pin the configured identity: %s", got)
	}
}

func TestControlPathFallsBackToShortRuntimeDirectory(t *testing.T) {
	longDirectory := filepath.Join(t.TempDir(), strings.Repeat("nested-directory-", 8))
	p := New(Config{SSHControlDir: longDirectory}, &procexec.FakeRunner{})
	path, err := p.controlPath("deployment-instance@ssh.railway.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(path) > 80 {
		t.Fatalf("control path remains too long (%d): %s", len(path), path)
	}
	if filepath.Dir(path) == longDirectory {
		t.Fatalf("long control directory was not replaced: %s", path)
	}
}

func TestRailwayStartCommandRunsEntrypointForFleetSlots(t *testing.T) {
	if got := railwayStartCommand(true); got != "/usr/local/bin/vmbox-entrypoint vmbox-runtime idle" {
		t.Fatalf("detached start command = %q", got)
	}
	if got := railwayStartCommand(false); got != "sleep infinity" {
		t.Fatalf("ordinary start command = %q", got)
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

func TestSuccessfulDeploymentWithExitedReplicaIsStopped(t *testing.T) {
	var exited service
	if err := json.Unmarshal([]byte(`{"id":"service-id","name":"vmbox-box","status":"SUCCESS","replicas":{"configured":1,"running":0,"crashed":0,"exited":1,"total":1}}`), &exited); err != nil {
		t.Fatal(err)
	}
	if got := serviceState(exited); got != provider.StateStopped {
		t.Fatalf("serviceState(exited SUCCESS) = %q, want %q", got, provider.StateStopped)
	}
	exited.Replicas.Running = 1
	exited.Replicas.Exited = 0
	if got := serviceState(exited); got != provider.StateRunning {
		t.Fatalf("serviceState(running SUCCESS) = %q, want %q", got, provider.StateRunning)
	}
}

func TestSuccessfulDeploymentWithCrashedReplicaIsFailed(t *testing.T) {
	var crashed service
	if err := json.Unmarshal([]byte(`{"id":"service-id","name":"vmbox-box","status":"SUCCESS","replicas":{"configured":1,"running":0,"crashed":1,"exited":0,"total":1}}`), &crashed); err != nil {
		t.Fatal(err)
	}
	if got := serviceState(crashed); got != provider.StateFailed {
		t.Fatalf("serviceState(crashed SUCCESS) = %q, want %q", got, provider.StateFailed)
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

func TestConnectImageUsesServiceInstanceUpdateForScopedTokens(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte("{\"data\":{\"serviceInstanceUpdate\":true}}")}}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment"}, runner)
	image := "ghcr.io/acme/worker@sha256:abc"
	if err := p.connectImage(context.Background(), "service-id", image); err != nil {
		t.Fatal(err)
	}
	call := runner.Calls[0].Argv
	if len(call) < 2 || call[0] != "railway" || call[1] != "api" || strings.Contains(strings.Join(call, " "), "service source connect") {
		t.Fatalf("image source did not use the GraphQL API: %#v", call)
	}
	variables := ""
	for i, value := range call {
		if value == "--variables" && i+1 < len(call) {
			variables = call[i+1]
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(variables), &decoded); err != nil {
		t.Fatalf("decode variables %q: %v", variables, err)
	}
	input, _ := decoded["input"].(map[string]any)
	source, _ := input["source"].(map[string]any)
	if decoded["serviceId"] != "service-id" || decoded["environmentId"] != "environment" || source["image"] != image {
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

// TestSanitizeSlotUndeploysComputeAndRetainsTheService pins 108e6a4: sanitizing
// a freed slot must remove only its running deployment. Resubmitting a
// deployment instead put the slot straight back into service, and deleting it
// would have destroyed fleet capacity the controller still tracks.
func TestSanitizeSlotUndeploysComputeAndRetainsTheService(t *testing.T) {
	running := `[{"id":"service-id","name":"slot-a-01","status":"SUCCESS"}]`
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(running)},
		{Stdout: []byte(`{"volumes":[{"id":"volume-1","serviceName":"other-slot","mountPath":"/data"}]}`)},
		{},
		{Stdout: []byte(`[{"id":"service-id","name":"slot-a-01","status":"NO_DEPLOYMENT"}]`)},
		{Stdout: []byte(`{"VMBOX_ACCOUNT_ID":"standalone","VMBOX_BOX_ID":"slot-a-01"}`)},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", PollInterval: time.Millisecond, ReadyTimeout: time.Second}, runner)
	if err := p.SanitizeSlot(context.Background(), "slot-a-01"); err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, call := range runner.Calls {
		command := strings.Join(call.Argv, " ")
		commands = append(commands, command)
		for _, destructive := range []string{" service delete ", " volume delete ", " redeploy "} {
			if strings.Contains(command, destructive) {
				t.Fatalf("sanitation ran a destructive or redeploying command: %s", command)
			}
		}
	}
	joined := strings.Join(commands, "\n")
	if !strings.Contains(joined, "railway down --service slot-a-01 --yes") {
		t.Fatalf("sanitation did not undeploy the slot:\n%s", joined)
	}
}

func TestSanitizeSlotLeavesAnAlreadyStoppedSlotAlone(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(`[{"id":"service-id","name":"slot-a-01","status":"NO_DEPLOYMENT"}]`)},
		{Stdout: []byte(`{"volumes":[]}`)},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", PollInterval: time.Millisecond, ReadyTimeout: time.Second}, runner)
	if err := p.SanitizeSlot(context.Background(), "slot-a-01"); err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.Calls {
		if strings.Contains(strings.Join(call.Argv, " "), " down ") {
			t.Fatalf("a stopped slot was powered down again: %v", call.Argv)
		}
	}
}

func TestSanitizeSlotRefusesWhileAWorkspaceIsStillAttached(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(`[{"id":"service-id","name":"slot-a-01","status":"SUCCESS"}]`)},
		{Stdout: []byte(`{"volumes":[{"id":"volume-1","serviceName":"slot-a-01","mountPath":"/data"}]}`)},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", PollInterval: time.Millisecond, ReadyTimeout: time.Second}, runner)
	err := p.SanitizeSlot(context.Background(), "slot-a-01")
	if err == nil || !strings.Contains(err.Error(), "remains attached") {
		t.Fatalf("err=%v", err)
	}
	for _, call := range runner.Calls {
		if strings.Contains(strings.Join(call.Argv, " "), " down ") {
			t.Fatal("sanitation powered down a slot that still carried a workspace")
		}
	}
}
