package verification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
)

// This subprocess is a real executable, launched with exact approved argv.
func TestCommand(t *testing.T) {
	args := os.Args
	pos := -1
	for i, a := range args {
		if a == "--verification-helper" {
			pos = i
			break
		}
	}
	if pos < 0 {
		return
	}
	args = args[pos+1:]
	switch args[0] {
	case "pass":
		os.Stdout.WriteString("passed\n")
		os.Stderr.WriteString("diagnostic\n")
	case "exit":
		os.Exit(7)
	case "signal":
		syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(time.Second)
	case "sleep":
		time.Sleep(20 * time.Second)
	case "tamper":
		os.WriteFile("tracked", []byte("modified"), 0600)
	case "untracked":
		os.WriteFile("new-file", []byte("new"), 0600)
	case "large":
		os.Stdout.WriteString(strings.Repeat("o", 3*LogLimit))
		os.Stderr.WriteString(strings.Repeat("e", 2*LogLimit))
		os.Exit(7)
	case "large-zero":
		os.Stdout.WriteString(strings.Repeat("x", 2*LogLimit))
	case "head":
		os.WriteFile(".git/HEAD", []byte(strings.Repeat("a", 40)+"\n"), 0600)
	case "delete-evidence":
		entries, _ := os.ReadDir(args[1])
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "verification-") {
				os.Remove(filepath.Join(args[1], entry.Name(), "000.stdout.log"))
			}
		}
	case "argv":
		json.NewEncoder(os.Stdout).Encode(args[1:])
	case "child":
		cmd := exec.Command(os.Args[0], "-test.run=^TestCommand$", "--", "--verification-helper", "sleep")
		if e := cmd.Start(); e != nil {
			os.Exit(9)
		}
		os.WriteFile(args[1], []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
		cmd.Wait()
	}
	os.Exit(0)
}
func fixture(t *testing.T) Request {
	t.Helper()
	root := t.TempDir()
	ws := filepath.Join(root, "source")
	out := filepath.Join(root, "evidence")
	os.Mkdir(ws, 0700)
	os.Mkdir(out, 0700)
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("/usr/bin/git", args...)
		c.Dir = ws
		c.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "--template=")
	os.WriteFile(filepath.Join(ws, "tracked"), []byte("original\n"), 0600)
	git("add", "tracked")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "fixture")
	return Request{Workspace: ws, ExpectedSHA: git("rev-parse", "HEAD"), OutputDir: out, Checks: []factory.Check{command(t, "pass")}}
}

func TestActualGoBuildWithPrivateCache(t *testing.T) {
	req := fixture(t)
	for name, body := range map[string]string{"go.mod": "module example.invalid/verificationfixture\n\ngo 1.26\n", "main.go": "package main\nfunc main() {}\n"} {
		if err := os.WriteFile(filepath.Join(req.Workspace, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		cmd := exec.Command("/usr/bin/git", args...)
		cmd.Dir = req.Workspace
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture Git: %v", err)
		}
		return strings.TrimSpace(string(b))
	}
	git("add", "go.mod", "main.go")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "real build fixture")
	req.ExpectedSHA = git("rev-parse", "HEAD")
	output := filepath.Join(req.OutputDir, "fixture-binary")
	req.Checks = []factory.Check{{Argv: []string{"go", "build", "-o", output, "."}, Cwd: ".", TimeoutSeconds: 120}}
	t.Setenv("GITHUB_TOKEN", "must-not-reach-check")
	r, err := Run(context.Background(), req)
	if err != nil || !r.AllPassed {
		t.Fatalf("actual Go build failed: %v; report error=%s", err, r.Error)
	}
	if st, err := os.Stat(output); err != nil || st.Size() == 0 {
		t.Fatal("compiler output missing")
	}
	evidence(t, r)
}
func command(t *testing.T, args ...string) factory.Check {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	return factory.Check{Argv: append([]string{exe, "-test.run=^TestCommand$", "--", "--verification-helper"}, args...), Cwd: ".", TimeoutSeconds: 5}
}
func evidence(t *testing.T, r Report) {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(r.EvidenceDir, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	var got Report
	if e = json.Unmarshal(b, &got); e != nil {
		t.Fatal(e)
	}
	if got.AllPassed != r.AllPassed {
		t.Fatal("manifest disagrees")
	}
	st, e := os.Stat(r.EvidenceDir)
	if e != nil || st.Mode().Perm() != 0700 {
		t.Fatal("evidence not private")
	}
	for _, c := range r.Checks {
		if !c.Executed {
			continue
		}
		for _, l := range []Log{c.Stdout, c.Stderr} {
			b, e := os.ReadFile(filepath.Join(r.EvidenceDir, l.File))
			if e != nil {
				t.Fatal(e)
			}
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != l.SHA256 || int64(len(b)) != l.Bytes || len(b) > LogLimit {
				t.Fatal("invalid log evidence")
			}
		}
	}
}
func TestExecution(t *testing.T) {
	for _, mode := range []string{"pass", "exit", "signal", "sleep", "tamper", "untracked", "large"} {
		t.Run(mode, func(t *testing.T) {
			req := fixture(t)
			req.Checks = []factory.Check{command(t, mode)}
			if mode == "sleep" {
				req.Checks[0].TimeoutSeconds = 1
			}
			r, e := Run(context.Background(), req)
			evidence(t, r)
			c := r.Checks[0]
			if !c.Executed || c.StartedAt == nil || c.FinishedAt == nil {
				t.Fatal("missing execution")
			}
			if r.AllPassed != (mode == "pass") {
				t.Fatalf("report %+v error %v", r, e)
			}
			switch mode {
			case "pass":
				if e != nil || c.ExitCode == nil || *c.ExitCode != 0 {
					t.Fatal(r, e)
				}
			case "exit", "large":
				if e != nil || c.ExitCode == nil || *c.ExitCode != 7 {
					t.Fatal(r, e)
				}
			case "signal":
				if c.Signal == 0 || c.ExitCode != nil {
					t.Fatal(r)
				}
			case "sleep":
				if !c.Timeout || c.Cancel || e == nil {
					t.Fatal(r, e)
				}
			case "tamper", "untracked":
				if r.SourceClean || e == nil {
					t.Fatal(r, e)
				}
			}
			if mode == "large" && (!c.Stdout.Truncated || !c.Stderr.Truncated) {
				t.Fatal("missing truncation")
			}
		})
	}
}
func TestExactArgvAndUniqueAttempts(t *testing.T) {
	req := fixture(t)
	args := []string{"space value", "$(touch injected)", "; exit 8", "", "'quoted'", "line\nbreak"}
	req.Checks = []factory.Check{command(t, append([]string{"argv"}, args...)...)}
	r, e := Run(context.Background(), req)
	if e != nil || !r.AllPassed {
		t.Fatal(r, e)
	}
	evidence(t, r)
	b, e := os.ReadFile(filepath.Join(r.EvidenceDir, r.Checks[0].Stdout.File))
	if e != nil {
		t.Fatal(e)
	}
	var got []string
	json.Unmarshal(b, &got)
	if strings.Join(got, "\x00") != strings.Join(args, "\x00") {
		t.Fatalf("argv %q", got)
	}
	r2, e := Run(context.Background(), req)
	if e != nil || !r2.AllPassed || r2.EvidenceDir == r.EvidenceDir {
		t.Fatal(r2, e)
	}
}
func TestUnsafeInputs(t *testing.T) {
	for _, mode := range []string{"traversal", "absolute", "symlink", "output", "output-symlink", "bad-sha", "dirty", "timeout", "empty", "missing-executable"} {
		t.Run(mode, func(t *testing.T) {
			req := fixture(t)
			switch mode {
			case "traversal":
				req.Checks[0].Cwd = "sub/../."
			case "absolute":
				req.Checks[0].Cwd = req.OutputDir
			case "symlink":
				os.Symlink(req.OutputDir, filepath.Join(req.Workspace, "link"))
				req.Checks[0].Cwd = "link"
			case "output":
				req.OutputDir = req.Workspace
			case "output-symlink":
				p := filepath.Join(filepath.Dir(req.OutputDir), "link")
				os.Symlink(req.OutputDir, p)
				req.OutputDir = p
			case "bad-sha":
				req.ExpectedSHA = strings.Repeat("a", 40)
			case "dirty":
				os.WriteFile(filepath.Join(req.Workspace, "tracked"), []byte("dirty"), 0600)
			case "timeout":
				req.Checks[0].TimeoutSeconds = 3601
			case "empty":
				req.Checks = nil
			case "missing-executable":
				req.Checks[0].Argv = []string{"/nonexistent/verification-command"}
			}
			r, e := Run(context.Background(), req)
			if e == nil || r.AllPassed {
				t.Fatal(r, e)
			}
			for _, c := range r.Checks {
				if c.Executed || c.ExitCode != nil {
					t.Fatal("invalid request executed")
				}
			}
		})
	}
}
func TestCancelOwnedGroupOnly(t *testing.T) {
	req := fixture(t)
	pidFile := filepath.Join(req.OutputDir, "child.pid")
	req.Checks = []factory.Check{command(t, "child", pidFile)}
	sibling := exec.Command(req.Checks[0].Argv[0], "-test.run=^TestCommand$", "--", "--verification-helper", "sleep")
	if e := sibling.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { sibling.Process.Kill(); sibling.Wait() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		r Report
		e error
	}
	done := make(chan result, 1)
	go func() { r, e := Run(ctx, req); done <- result{r, e} }()
	var childPID int
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		b, e := os.ReadFile(pidFile)
		if e == nil {
			childPID, _ = strconv.Atoi(string(b))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("child did not start")
	}
	cancel()
	res := <-done
	evidence(t, res.r)
	if res.e == nil || res.r.AllPassed || !res.r.Checks[0].Cancel || res.r.Checks[0].Timeout {
		t.Fatal(res)
	}
	if e := sibling.Process.Signal(syscall.Signal(0)); e != nil {
		t.Fatal("sibling killed", e)
	}
	// A killed orphan may remain a zombie until this box's PID 1 reaps it.
	b, e := os.ReadFile("/proc/" + strconv.Itoa(childPID) + "/stat")
	if e == nil {
		end := strings.LastIndex(string(b), ")")
		if end < 0 || !strings.HasPrefix(string(b)[end+1:], " Z") {
			t.Fatalf("owned descendant survived: %s", b)
		}
	}
}
func TestGitConfigIgnored(t *testing.T) {
	req := fixture(t)
	marker := filepath.Join(req.OutputDir, "hook-ran")
	hook := filepath.Join(req.OutputDir, "hook")
	os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0700)
	config := "[core]\nrepositoryformatversion = 0\nbare = false\nfsmonitor = " + hook + "\nhooksPath = " + req.OutputDir + "\n[include]\npath = /nonexistent/config\n[diff]\nexternal = " + hook + "\n[filter \"evil\"]\nclean = " + hook + "\nrequired = true\n"
	os.WriteFile(filepath.Join(req.Workspace, ".git/config"), []byte(config), 0600)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	t.Setenv("GIT_CONFIG_VALUE_0", hook)
	r, e := Run(context.Background(), req)
	if e != nil || !r.AllPassed {
		t.Fatal(r, e)
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("repository or inherited config executed")
	}
}

func TestEssentialEvidenceAndSource(t *testing.T) {
	for _, mode := range []string{"large-zero", "head", "delete-evidence"} {
		t.Run(mode, func(t *testing.T) {
			req := fixture(t)
			req.Checks = append(req.Checks, command(t, mode, req.OutputDir))
			r, e := Run(context.Background(), req)
			if r.AllPassed || len(r.Checks) != 2 || !r.Checks[1].Executed {
				t.Fatal(r, e)
			}
			if mode == "large-zero" {
				if e != nil || r.Checks[1].ExitCode == nil || *r.Checks[1].ExitCode != 0 || !r.Checks[1].Stdout.Truncated {
					t.Fatal(r, e)
				}
			} else if e == nil {
				t.Fatal("missing error")
			}
			b, err := os.ReadFile(filepath.Join(r.EvidenceDir, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			var persisted Report
			json.Unmarshal(b, &persisted)
			if persisted.AllPassed {
				t.Fatal("false verification persisted")
			}
		})
	}
}
func TestStagedAndNormalizedSource(t *testing.T) {
	for _, mode := range []string{"staged", "normalized", "ignored"} {
		t.Run(mode, func(t *testing.T) {
			req := fixture(t)
			git := func(args ...string) {
				t.Helper()
				c := exec.Command("/usr/bin/git", args...)
				c.Dir = req.Workspace
				c.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
				if b, e := c.CombinedOutput(); e != nil {
					t.Fatalf("%v: %s", e, b)
				}
			}
			switch mode {
			case "staged":
				os.WriteFile(filepath.Join(req.Workspace, "tracked"), []byte("staged\n"), 0600)
				git("add", "tracked")
				os.WriteFile(filepath.Join(req.Workspace, "tracked"), []byte("original\n"), 0600)
			case "normalized":
				os.WriteFile(filepath.Join(req.Workspace, ".git/info/attributes"), []byte("* text\n"), 0600)
				os.WriteFile(filepath.Join(req.Workspace, "tracked"), []byte("original\r\n"), 0600)
			case "ignored":
				os.MkdirAll(filepath.Join(req.Workspace, ".git/info"), 0700)
				os.WriteFile(filepath.Join(req.Workspace, ".git/info/exclude"), []byte("ignored\n"), 0600)
				os.WriteFile(filepath.Join(req.Workspace, "ignored"), []byte("data"), 0600)
			}
			r, e := Run(context.Background(), req)
			if e == nil || r.AllPassed || r.Checks[0].Executed {
				t.Fatal(r, e)
			}
		})
	}
}
func TestRealExecutableAndEnvironment(t *testing.T) {
	req := fixture(t)
	req.Checks = []factory.Check{{Argv: []string{"printf", "%s", "$(no interpolation); spaces"}, Cwd: ".", TimeoutSeconds: 2}, {Argv: []string{"/usr/bin/env"}, Cwd: ".", TimeoutSeconds: 2}}
	t.Setenv("PUBLISHING_TOKEN", "must-not-leak")
	t.Setenv("PATH", "/nonexistent")
	r, e := Run(context.Background(), req)
	if e != nil || !r.AllPassed {
		t.Fatal(r, e)
	}
	evidence(t, r)
	b, _ := os.ReadFile(filepath.Join(r.EvidenceDir, r.Checks[0].Stdout.File))
	if string(b) != "$(no interpolation); spaces" {
		t.Fatalf("argv changed: %s", b)
	}
	b, _ = os.ReadFile(filepath.Join(r.EvidenceDir, r.Checks[1].Stdout.File))
	if strings.Contains(string(b), "must-not-leak") {
		t.Fatal("inherited credential")
	}
}

func TestChangedValidHEADStopsRemainingChecks(t *testing.T) {
	req := fixture(t)
	cmd := exec.Command("/usr/bin/git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit-tree", "HEAD^{tree}", "-p", "HEAD", "-m", "other commit")
	cmd.Dir = req.Workspace
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	b, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("commit-tree: %v %s", e, b)
	}
	other := strings.TrimSpace(string(b))
	req.Checks = []factory.Check{{Argv: []string{"/usr/bin/git", "update-ref", "HEAD", other}, Cwd: ".", TimeoutSeconds: 3}, command(t, "pass")}
	r, e := Run(context.Background(), req)
	if e == nil || r.AllPassed || r.SourceSHA != req.ExpectedSHA || r.FinalSourceSHA != other || !r.Checks[0].Executed || r.Checks[1].Executed || r.Checks[1].ExitCode != nil {
		t.Fatal(r, e)
	}
	evidence(t, r)
}
func TestPreCancelledNeverExecutes(t *testing.T) {
	req := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, e := Run(ctx, req)
	if e == nil || r.AllPassed || r.Checks[0].Executed || r.Checks[0].ExitCode != nil {
		t.Fatal(r, e)
	}
	evidence(t, r)
}
