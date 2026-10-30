package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	hubservice "github.com/0xikarus/vmbox-service/internal/hub"
	"github.com/0xikarus/vmbox-service/internal/procexec"
)

func TestHubInstallRequiresAutostartOptIn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		flags  []string
		policy string
	}{
		{"default off", nil, "no"},
		{"explicit on", []string{"--autostart"}, "unless-stopped"},
		{"explicit off", []string{"--autostart=false"}, "no"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New()
			a.Out = &bytes.Buffer{}
			a.Err = &bytes.Buffer{}
			a.IsTerminal = func() bool { return false }
			a.WorkingDir, _ = filepath.Abs("../..")
			password := filepath.Join(t.TempDir(), "password")
			if err := os.WriteFile(password, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			c := hubservice.Config{ID: "hub-" + strings.Repeat("b", 20), APIToken: strings.Repeat("a", 64), Listen: "127.0.0.1:8091", Image: "fixture:worker"}
			data, _ := json.Marshal(map[string]any{"config": c, "dir": t.TempDir()})
			runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: data}, {}, {}, {}}}
			a.Runner = runner
			args := append([]string{"hub", "install", "--password-file", password, "--no-dialog"}, tc.flags...)
			if err := a.Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			if len(runner.Calls) != 4 {
				t.Fatal("unexpected installer operations")
			}
			setup := runner.Calls[3]
			// Execute the real installer script against a fake Docker/host boundary so
			// this verifies its actual restart argument without creating any resources.
			harness := `import json,sys,subprocess,os,stat,types
calls=[]
original_stat=os.stat
def fake_stat(p,*args,**kwargs):
 if str(p)=='/var/run/docker.sock':return types.SimpleNamespace(st_mode=stat.S_IFSOCK,st_gid=1000)
 return original_stat(p,*args,**kwargs)
def run(argv,**kwargs):
 calls.append(argv)
 return types.SimpleNamespace(returncode=1 if argv[1]=='inspect' else 0,stdout='')
os.stat=fake_stat
subprocess.run=run
subprocess.check_output=lambda *args,**kwargs:json.dumps({'OperatingSystem':'Linux','DockerRootDir':'/fixture/docker'})
exec(sys.argv[1])
print(json.dumps(calls))`
			cmd := exec.Command("python3", "-c", harness, setup.Argv[2])
			cmd.Stdin = strings.NewReader(setup.Stdin)
			out, err := cmd.Output()
			if err != nil {
				t.Fatal("installer script failed against fake Docker boundary:", err)
			}
			lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
			var calls [][]string
			if json.Unmarshal(lines[len(lines)-1], &calls) != nil {
				t.Fatal("invalid installer operations")
			}
			found := false
			for _, argv := range calls {
				if len(argv) > 1 && argv[1] == "run" {
					for i, arg := range argv {
						if arg == "--restart" {
							found = true
							if argv[i+1] != tc.policy {
								t.Fatalf("restart policy = %s; want %s", argv[i+1], tc.policy)
							}
						}
					}
				}
			}
			if !found {
				t.Fatal("restart policy was not set explicitly")
			}
		})
	}
}

func TestHubUpUsesOwnedContainerWithoutAPIOrRebuild(t *testing.T) {
	a := New()
	a.Out = &bytes.Buffer{}
	a.Err = &bytes.Buffer{}
	a.IsTerminal = func() bool { return false }
	c := hubservice.Config{ID: "hub-" + strings.Repeat("b", 20), APIToken: strings.Repeat("a", 64)}
	data, _ := json.Marshal(map[string]any{"config": c, "dir": "/fixture/hub"})
	r := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: data}, {}}}
	a.Runner = r
	if err := a.Run(context.Background(), []string{"hub", "up", "--host", "fixture-host"}); err != nil {
		t.Fatal(err)
	}
	if len(r.Calls) != 2 || r.Calls[1].Argv[0] != "ssh" {
		t.Fatal("manual start must use SSH and avoid rebuilds/API")
	}
	var input map[string]string
	if json.Unmarshal([]byte(r.Calls[1].Stdin), &input) != nil || input["id"] != c.ID || input["action"] != "start" {
		t.Fatal("manual start did not target the owned manager")
	}
	if strings.Contains(r.Calls[1].Stdin, c.APIToken) {
		t.Fatal("API credential sent to lifecycle helper")
	}
}
