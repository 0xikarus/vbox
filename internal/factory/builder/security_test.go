package builder

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func awaitFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, e := os.ReadFile(path); e == nil && len(b) > 0 {
			return b
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("process did not become ready")
	return nil
}
func TestOwnedProcessGroup(t *testing.T) {
	for _, cancellation := range []bool{false, true} {
		name := "normal-leader-exit"
		if cancellation {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			r := request(t)
			dir := t.TempDir()
			pidFile, release := filepath.Join(dir, "child.pid"), filepath.Join(dir, "release")
			sibling := exec.Command("sleep", "60")
			if e := sibling.Start(); e != nil {
				t.Fatal(e)
			}
			defer func() { sibling.Process.Kill(); sibling.Wait() }()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			body := commitFixture + "sleep 60 &\necho $! > '" + pidFile + "'\nwhile [ ! -f '" + release + "' ]; do sleep 0.01; done\n"
			type outcome struct {
				result Result
				err    error
			}
			done := make(chan outcome, 1)
			executable := controlled(t, body)
			go func() { r, e := run(ctx, r, executable); done <- outcome{r, e} }()
			pid, e := strconv.Atoi(strings.TrimSpace(string(awaitFile(t, pidFile))))
			if e != nil || pid <= 0 {
				t.Fatal("invalid child PID")
			}
			if cancellation {
				cancel()
			} else if e := os.WriteFile(release, []byte("go"), 0600); e != nil {
				t.Fatal(e)
			}
			var got outcome
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("cleanup blocked on child holding output pipes")
			}
			if cancellation {
				if !errors.Is(got.err, context.Canceled) || got.result.Signal != 9 || got.result.CandidateSHA != "" {
					t.Fatalf("%+v", got)
				}
			} else if got.err != nil || got.result.ExitCode == nil || *got.result.ExitCode != 0 || got.result.CandidateSHA == "" {
				t.Fatalf("%+v", got)
			}
			// Killed orphans may remain zombies under the box's PID 1.
			deadline := time.Now().Add(time.Second)
			for {
				b, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
				if os.IsNotExist(e) {
					break
				}
				end := strings.LastIndex(string(b), ")")
				if e == nil && end >= 0 && strings.HasPrefix(string(b)[end+1:], " Z") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("owned child survived: %s, %v", b, e)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if e := sibling.Process.Signal(syscall.Signal(0)); e != nil {
				t.Fatal("unrelated sibling killed", e)
			}
			t.Logf("leader signal=%d exit=%v; owned child stopped; unrelated sibling alive", got.result.Signal, exitValue(got.result))
		})
	}
}

func TestAgentCredentialEnvironment(t *testing.T) {
	r := request(t)
	blocked := []string{"VMBOX_CONTROLLER_TOKEN", "VMBOX_EVENT_KEY", "CONTROLLER_TOKEN", "FACTORY_PUBLISH_TOKEN", "VMBOX_FACTORY_TOKEN", "RAILWAY_TOKEN", "RAILWAY_API_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GIT_CONFIG_COUNT"}
	for _, key := range blocked {
		t.Setenv(key, "sentinel")
	}
	preserved := []string{"HOME", "XDG_CONFIG_HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"}
	// Verify the inherited values without changing HOME used by the Git fixtures.
	for _, key := range preserved[1:] {
		t.Setenv(key, "agent-saved-config")
	}
	capture := filepath.Join(t.TempDir(), "env")
	got, e := run(context.Background(), r, controlled(t, "env > '"+capture+"'\n"+commitFixture))
	if e != nil {
		t.Fatalf("%+v %v", got, e)
	}
	b, e := os.ReadFile(capture)
	if e != nil {
		t.Fatal(e)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(line, "=")
		env[k] = v
	}
	for _, key := range blocked {
		if _, ok := env[key]; ok {
			t.Errorf("credential inherited: %s", key)
		}
	}
	for _, key := range preserved {
		if env[key] != os.Getenv(key) {
			t.Errorf("agent config removed: %s", key)
		}
	}
}

func TestInspectionDoesNotExecuteLocalConfig(t *testing.T) {
	r := request(t)
	marker := filepath.Join(t.TempDir(), "executed")
	driver := controlled(t, "touch '"+marker+"'\n")
	// Configure after committing attributes, then force status to read worktree bytes.
	os.WriteFile(filepath.Join(r.Workspace, ".gitattributes"), []byte("*.js filter=hostile diff=hostile\n"), 0600)
	gitTest(t, r.Workspace, "add", ".")
	gitTest(t, r.Workspace, "commit", "-qm", "attributes")
	base := gitTest(t, r.Workspace, "rev-parse", "HEAD")
	include := filepath.Join(t.TempDir(), "config")
	os.WriteFile(include, []byte("[filter \"hostile\"]\n clean = "+driver+"\n smudge = "+driver+"\n required = true\n[diff \"hostile\"]\n textconv = "+driver+"\n command = "+driver+"\n"), 0600)
	gitTest(t, r.Workspace, "config", "include.path", include)
	gitTest(t, r.Workspace, "config", "core.fsmonitor", driver)
	os.WriteFile(filepath.Join(r.Workspace, "math.js"), []byte("changed raw bytes\n"), 0600)
	status, e := inspect(context.Background(), r.Workspace, "status", "--porcelain=v1", "--untracked-files=all", "--ignored", "--ignore-submodules=none")
	if e != nil || status == "" {
		t.Fatalf("dirty source not detected: %q %v", status, e)
	}
	if _, e := inspect(context.Background(), r.Workspace, "diff", "--numstat", "--no-ext-diff", "--no-textconv", base, "--"); e != nil {
		t.Fatal(e)
	}
	if e := createBranch(context.Background(), r.Workspace, r.Branch, base); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("local executable configuration ran", e)
	}
}

func TestInspectionIgnoresIndexTrustFlags(t *testing.T) {
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		t.Run(flag, func(t *testing.T) {
			r := request(t)
			gitTest(t, r.Workspace, "update-index", flag, "math.js")
			os.WriteFile(filepath.Join(r.Workspace, "math.js"), []byte("dirty\n"), 0600)
			status, e := inspect(context.Background(), r.Workspace, "status", "--porcelain=v1")
			if e != nil || status == "" {
				t.Fatalf("hidden change: %q %v", status, e)
			}
		})
	}
}

func TestLateCancellationCannotSignalReusedIdentity(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "true")
	group := prepareProcess(cmd, time.Second)
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	if e := group.wait(cmd); e != nil {
		t.Fatal(e)
	}
	sibling := exec.Command("sleep", "60")
	sibling.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if e := sibling.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { sibling.Process.Kill(); sibling.Wait() }()
	// Model a recycled PID deterministically; the closed ownership gate must
	// prevent any signal even if the process identity now belongs to a sibling.
	cmd.Process = sibling.Process
	if e := cmd.Cancel(); !errors.Is(e, os.ErrProcessDone) {
		t.Fatalf("late cancellation: %v", e)
	}
	b, e := os.ReadFile("/proc/" + strconv.Itoa(sibling.Process.Pid) + "/stat")
	end := strings.LastIndex(string(b), ")")
	if e != nil || end < 0 || strings.HasPrefix(string(b)[end+1:], " Z") {
		t.Fatal("sibling not alive")
	}
}
