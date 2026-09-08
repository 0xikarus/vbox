package staging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/transport"
)

type fixture struct {
	t          *testing.T
	root, path string
	mu         sync.Mutex
	endpoints  []string
	fault      string
}

func (f *fixture) Run(context.Context, []string, io.Reader, io.Writer, io.Writer) (procexec.Result, error) {
	f.t.Fatal("capturing transport used")
	return procexec.Result{}, nil
}
func (f *fixture) RunAttached(ctx context.Context, a []string, in io.Reader, out, errout io.Writer) (procexec.Result, error) {
	f.mu.Lock()
	f.endpoints = append(f.endpoints, a[len(a)-2])
	f.mu.Unlock()
	cmdline := a[len(a)-1]
	for _, s := range []string{"ghs_fixture_private", `{"private":"job"}`, "private-image", "fixture-binary"} {
		if strings.Contains(strings.Join(a, " "), s) {
			f.t.Error("payload in SSH argv")
		}
	}
	if a[0] != "ssh" || !strings.Contains(strings.Join(a, " "), "ControlMaster=no") || out != io.Discard || errout != io.Discard {
		f.t.Error("unexpected SSH contract")
	}
	// Execute the exact SSH-quoted command in a real shell. Only the fixed root
	// and PATH are redirected for isolation and the local Git network surrogate.
	cmdline = strings.ReplaceAll(cmdline, "/data/workspace/.vmbox-factory", f.root)
	cmdline = strings.ReplaceAll(cmdline, "PATH=/usr/bin:/bin", "PATH="+f.path+":/usr/bin:/bin")
	if f.fault != "" {
		b, e := io.ReadAll(in)
		if e != nil {
			return procexec.Result{}, e
		}
		if f.fault == "truncate" {
			b = b[:len(b)-1]
		} else {
			b[len(b)-1] ^= 1
		}
		in = bytes.NewReader(b)
	}
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-c", cmdline)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = errout
	err := cmd.Run()
	if err != nil {
		return procexec.Result{ExitCode: 1}, err
	}
	return procexec.Result{}, nil
}
func write(t *testing.T, p string, b []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(p, b, mode); err != nil {
		t.Fatal(err)
	}
}
func command(t *testing.T, args ...string) string {
	t.Helper()
	c := exec.Command(args[0], args[1:]...)
	c.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("%s: %v %s", args[0], e, b)
	}
	return strings.TrimSpace(string(b))
}
func setup(t *testing.T) (Stager, Input, *fixture, string) {
	t.Helper()
	base, e := os.MkdirTemp("/data/workspace", "staging-test-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	source := filepath.Join(base, "source")
	command(t, "git", "init", "--quiet", "--template=", source)
	write(t, filepath.Join(source, "file.txt"), []byte("source contents\n"), 0600)
	write(t, filepath.Join(source, ".gitattributes"), []byte("file.txt filter=lfs\n"), 0600)
	write(t, filepath.Join(source, ".gitmodules"), []byte("[submodule \"bad\"]\n path = bad\n url = https://invalid.invalid/bad\n"), 0600)
	command(t, "git", "-C", source, "add", ".")
	command(t, "git", "-C", source, "commit", "--quiet", "-m", "fixture")
	sha := command(t, "git", "-C", source, "rev-parse", "HEAD")
	bindir := filepath.Join(base, "tools")
	if e := os.Mkdir(bindir, 0700); e != nil {
		t.Fatal(e)
	}
	// This wrapper inspects real fetch argv/environment and redirects only its
	// network destination to a local repository. Init and checkout use real Git.
	wrapper := fmt.Sprintf(`#!/bin/bash
set -eu
printf '%%s\n' "$*" >> '%s/git-calls'
if [[ " $* " == *" fetch "* ]]; then
 [[ "$GIT_CONFIG_VALUE_0" == 'AUTHORIZATION: basic '* ]] || exit 71
 [[ "$GIT_CONFIG_KEY_1" == credential.helper && -z "$GIT_CONFIG_VALUE_1" ]] || exit 72
 [[ "$GIT_TERMINAL_PROMPT" == 0 && "$GIT_ASKPASS" == /bin/false && "$GIT_ALLOW_PROTOCOL" == https ]] || exit 73
 [[ "$GIT_CONFIG_VALUE_4" == false && "$GIT_CONFIG_VALUE_2" == /dev/null ]] || exit 74
 [[ "$*" == *'https://github.com/owner/repo.git'* ]] || exit 75
 [[ ! -e '%s/fail-fetch' ]] || exit 76
 args=(); for a in "$@"; do if [[ "$a" == https://github.com/owner/repo.git ]]; then args+=('%s'); else args+=("$a"); fi; done
 export GIT_ALLOW_PROTOCOL=file
 exec /usr/bin/git "${args[@]}"
fi
[[ -z "${GIT_CONFIG_VALUE_0:-}" ]] || exit 77
exec /usr/bin/git "$@"
`, base, base, source)
	write(t, filepath.Join(bindir, "git"), []byte(wrapper), 0700)
	f := &fixture{t: t, root: filepath.Join(base, "factory"), path: bindir}
	binary := filepath.Join(base, "binary")
	write(t, binary, []byte("fixture-binary\x00\xff"), 0600)
	s := Stager{SSH: transport.SSH{Runner: f}, BinaryPath: binary}
	in := Input{Connection: provider.Connection{Transport: "openssh", Endpoint: "user@current-host"}, AttemptID: strings.Repeat("a", 32), Repository: "owner/repo", BaseSHA: sha, SourceToken: "ghs_fixture_private", Job: []byte(`{"private":"job"}`), Images: []Image{{ID: strings.Repeat("b", 32), Data: []byte("private-image\x00\xff")}}}
	return s, in, f, base
}
func mustStage(t *testing.T, s Stager, in Input) {
	t.Helper()
	if e := s.Stage(context.Background(), in); e != nil {
		t.Fatal(e)
	}
}
func TestStageLocalShell(t *testing.T) {
	s, in, f, base := setup(t)
	mustStage(t, s, in)
	attempt := filepath.Join(f.root, "attempts", in.AttemptID)
	for p, want := range map[string][]byte{"job.json": in.Job, "images/" + in.Images[0].ID: in.Images[0].Data, "repo/file.txt": []byte("source contents\n")} {
		b, e := os.ReadFile(filepath.Join(attempt, p))
		if e != nil || !bytes.Equal(b, want) {
			t.Fatalf("incorrect %s: %v", p, e)
		}
	}
	for p, mode := range map[string]os.FileMode{attempt: 0700, filepath.Join(attempt, "images"): 0700, filepath.Join(attempt, "job.json"): 0600, filepath.Join(attempt, "images", in.Images[0].ID): 0600, filepath.Join(f.root, "bin/vmbox-planner"): 0700} {
		st, e := os.Stat(p)
		if e != nil || st.Mode().Perm() != mode {
			t.Fatalf("mode %s: %v", p, e)
		}
	}
	ready, _ := os.Stat(filepath.Join(attempt, "ready"))
	write(t, filepath.Join(attempt, "agent-result"), []byte("running work"), 0600)
	write(t, filepath.Join(attempt, "repo/file.txt"), []byte("agent changes"), 0600)
	in.SourceToken = "ghs_rotated_private"
	in.Connection.Endpoint = "user@new-current-host"
	mustStage(t, s, in)
	after, _ := os.Stat(filepath.Join(attempt, "ready"))
	if !os.SameFile(ready, after) {
		t.Fatal("retry replaced ready")
	}
	if b, _ := os.ReadFile(filepath.Join(attempt, "repo/file.txt")); string(b) != "agent changes" {
		t.Fatal("overwrote running repo")
	}
	if f.endpoints[1] != "user@new-current-host" {
		t.Fatal("cached endpoint")
	}
	calls, _ := os.ReadFile(filepath.Join(base, "git-calls"))
	if strings.Count(string(calls), " fetch ") != 1 {
		t.Fatal("retry fetched again")
	}
	filepath.WalkDir(f.root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			t.Fatal(e)
		}
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			if bytes.Contains(b, []byte("ghs_fixture_private")) || bytes.Contains(b, []byte("eC1hY2Nlc3MtdG9rZW46")) {
				t.Errorf("credential persisted in %s", p)
			}
		}
		return nil
	})
	for _, mutate := range []func(*Input){func(i *Input) { i.Job = []byte("changed") }, func(i *Input) { i.Repository = "owner/other" }, func(i *Input) { i.BaseSHA = strings.Repeat("f", 40) }, func(i *Input) { i.Images = []Image{{ID: strings.Repeat("b", 32), Data: []byte("changed")}} }} {
		changed := in
		mutate(&changed)
		if s.Stage(context.Background(), changed) == nil {
			t.Fatal("changed attempt accepted")
		}
	}
	write(t, s.BinaryPath, []byte("changed binary"), 0600)
	if s.Stage(context.Background(), in) == nil {
		t.Fatal("changed binary accepted")
	}
}
func TestRecoveryAndConcurrentStage(t *testing.T) {
	s, in, f, base := setup(t)
	write(t, filepath.Join(base, "fail-fetch"), nil, 0600)
	if s.Stage(context.Background(), in) == nil {
		t.Fatal("fetch failure accepted")
	}
	if _, e := os.Lstat(filepath.Join(f.root, "attempts", in.AttemptID)); !os.IsNotExist(e) {
		t.Fatal("partial attempt published")
	}
	changed := in
	changed.Job = []byte("new")
	if s.Stage(context.Background(), changed) == nil {
		t.Fatal("changed interrupted attempt accepted")
	}
	os.Remove(filepath.Join(base, "fail-fetch"))
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.Stage(context.Background(), in) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	calls, _ := os.ReadFile(filepath.Join(base, "git-calls"))
	if strings.Count(string(calls), " fetch ") != 2 {
		t.Fatal("concurrent duplicate fetch")
	}
}
func TestUnsafePathsAndTampering(t *testing.T) {
	for _, target := range []string{"root", "lock", "bin", "job", "image", "repo", "ready", "hardlink", "fifo"} {
		t.Run(target, func(t *testing.T) {
			s, in, f, base := setup(t)
			if target == "root" {
				os.Symlink(base, f.root)
			} else if target == "lock" {
				os.Mkdir(f.root, 0700)
				os.Symlink(filepath.Join(base, "binary"), filepath.Join(f.root, ".staging.lock"))
			} else {
				mustStage(t, s, in)
				paths := map[string]string{"bin": "bin/vmbox-planner", "job": "attempts/" + in.AttemptID + "/job.json", "image": "attempts/" + in.AttemptID + "/images/" + in.Images[0].ID, "repo": "attempts/" + in.AttemptID + "/repo", "ready": "attempts/" + in.AttemptID + "/ready", "hardlink": "attempts/" + in.AttemptID + "/job.json", "fifo": "attempts/" + in.AttemptID + "/job.json"}
				p := filepath.Join(f.root, paths[target])
				os.RemoveAll(p)
				if target == "hardlink" {
					os.Link(s.BinaryPath, p)
				} else if target == "fifo" {
					command(t, "mkfifo", p)
				} else {
					os.Symlink(base, p)
				}
			}
			if s.Stage(context.Background(), in) == nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
}
func TestValidation(t *testing.T) {
	s, in, f, _ := setup(t)
	cases := []func(*Input){func(i *Input) { i.AttemptID = strings.Repeat("A", 32) }, func(i *Input) { i.Repository = "a/../b" }, func(i *Input) { i.Repository = "a/-b" }, func(i *Input) { i.BaseSHA = strings.Repeat("A", 40) }, func(i *Input) { i.SourceToken = "bad\nsecret" }, func(i *Input) { i.SourceToken = "" }, func(i *Input) { i.Job = make([]byte, MaxJobBytes+1) }, func(i *Input) { i.Images = []Image{{ID: "../bad"}} }, func(i *Input) { i.Images = append(i.Images, i.Images[0]) }, func(i *Input) { i.Images = []Image{{ID: strings.Repeat("b", 32), Data: make([]byte, MaxImageBytes+1)}} }, func(i *Input) {
		i.Images = nil
		for n := 0; n < 5; n++ {
			i.Images = append(i.Images, Image{ID: fmt.Sprintf("%032x", n), Data: make([]byte, MaxImageBytes)})
		}
	}}
	for n, c := range cases {
		bad := in
		c(&bad)
		if s.Stage(context.Background(), bad) == nil {
			t.Fatalf("case %d accepted", n)
		}
	}
	if len(f.endpoints) != 0 {
		t.Fatal("validation reached SSH")
	}
	link := s.BinaryPath + "-link"
	os.Symlink(s.BinaryPath, link)
	s.BinaryPath = link
	if s.Stage(context.Background(), in) == nil {
		t.Fatal("binary symlink accepted")
	}
}

type failureRunner struct{}

func (failureRunner) Run(context.Context, []string, io.Reader, io.Writer, io.Writer) (procexec.Result, error) {
	return procexec.Result{}, errors.New("secret detail")
}
func (failureRunner) RunAttached(context.Context, []string, io.Reader, io.Writer, io.Writer) (procexec.Result, error) {
	return procexec.Result{Stdout: []byte("secret detail"), Stderr: []byte("secret detail"), ExitCode: 1}, errors.New("secret detail")
}
func TestFailureRedacted(t *testing.T) {
	s, in, _, _ := setup(t)
	s.SSH.Runner = failureRunner{}
	e := s.Stage(context.Background(), in)
	if e == nil || strings.Contains(e.Error(), "secret detail") {
		t.Fatal("error leakage")
	}
}

func TestInterruptedTransfer(t *testing.T) {
	for _, fault := range []string{"truncate", "corrupt"} {
		t.Run(fault, func(t *testing.T) {
			s, in, f, _ := setup(t)
			f.fault = fault
			if s.Stage(context.Background(), in) == nil {
				t.Fatal("incomplete/corrupt transfer accepted")
			}
			if _, e := os.Stat(filepath.Join(f.root, "attempts", in.AttemptID)); !os.IsNotExist(e) {
				t.Fatal("partial attempt published")
			}
			changed := in
			changed.Job = []byte("changed")
			if s.Stage(context.Background(), changed) == nil {
				t.Fatal("reservation lost")
			}
			f.fault = ""
			mustStage(t, s, in)
		})
	}
}
func TestNoCredentialOrExecutionFallback(t *testing.T) {
	s, in, _, base := setup(t)
	sentinel := filepath.Join(base, "executed")
	poison := filepath.Join(base, "poison")
	write(t, poison, []byte("[filter \"lfs\"]\n smudge = touch "+sentinel+"\n required = true\n[credential]\n helper = !touch "+sentinel+"\n[core]\n hooksPath = "+base+"\n fsmonitor = touch "+sentinel+"\n[url \"https://invalid.invalid/\"]\n insteadOf = https://github.com/\n"), 0600)
	write(t, filepath.Join(base, "post-checkout"), []byte("#!/bin/sh\ntouch "+sentinel+"\n"), 0700)
	write(t, filepath.Join(base, "bashenv"), []byte("touch "+sentinel+"\n"), 0600)
	// The outer test shell is intentionally started before the clean remote env;
	// do not set BASH_ENV here (sshd's login shell is outside this API's control).
	t.Setenv("GIT_CONFIG_GLOBAL", poison)
	t.Setenv("GIT_CONFIG_SYSTEM", poison)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "credential.helper")
	t.Setenv("GIT_CONFIG_VALUE_0", "!touch "+sentinel)
	t.Setenv("GIT_ASKPASS", filepath.Join(base, "post-checkout"))
	t.Setenv("https_proxy", "http://invalid.invalid")
	mustStage(t, s, in)
	if _, e := os.Stat(sentinel); !os.IsNotExist(e) {
		t.Fatal("untrusted git execution")
	}
}
func TestBoundsAndPinnedBinary(t *testing.T) {
	s, in, f, _ := setup(t)
	in.Job = bytes.Repeat([]byte("j"), MaxJobBytes)
	in.Images = nil
	for n := 3; n >= 0; n-- {
		in.Images = append(in.Images, Image{ID: fmt.Sprintf("%032x", n), Data: bytes.Repeat([]byte{byte(n)}, MaxImageBytes)})
	}
	mustStage(t, s, in)
	// Reordering and a rotating token do not alter stable content.
	in.Images[0], in.Images[3] = in.Images[3], in.Images[0]
	in.SourceToken = "ghs_rotated"
	mustStage(t, s, in)
	original, _ := os.ReadFile(filepath.Join(f.root, "bin/vmbox-planner"))
	write(t, s.BinaryPath, []byte("new binary"), 0600)
	in.AttemptID = strings.Repeat("c", 32)
	if s.Stage(context.Background(), in) == nil {
		t.Fatal("replaced shared binary")
	}
	after, _ := os.ReadFile(filepath.Join(f.root, "bin/vmbox-planner"))
	if !bytes.Equal(original, after) {
		t.Fatal("overwrote planner")
	}
	if e := os.Truncate(s.BinaryPath, MaxBinaryBytes+1); e != nil {
		t.Fatal(e)
	}
	before := len(f.endpoints)
	if s.Stage(context.Background(), in) == nil || len(f.endpoints) != before {
		t.Fatal("oversized binary reached SSH")
	}
}
func TestMissingPublishedPathNotRecreated(t *testing.T) {
	s, in, f, _ := setup(t)
	mustStage(t, s, in)
	p := filepath.Join(f.root, "attempts", in.AttemptID, "repo")
	os.RemoveAll(p)
	if s.Stage(context.Background(), in) == nil {
		t.Fatal("missing published repo accepted")
	}
	if _, e := os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("mutated existing attempt")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s.Stage(ctx, in) == nil {
		t.Fatal("canceled stage succeeded")
	}
}
func TestBuiltPlanner(t *testing.T) {
	binary := os.Getenv("STAGING_TEST_BINARY")
	if binary == "" {
		t.Skip("set STAGING_TEST_BINARY to a planner built locally for real-binary transfer")
	}
	s, in, f, _ := setup(t)
	s.BinaryPath = binary
	mustStage(t, s, in)
	want, e := os.ReadFile(binary)
	if e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(filepath.Join(f.root, "bin/vmbox-planner"))
	if e != nil || !bytes.Equal(got, want) {
		t.Fatal("built binary transfer mismatch")
	}
}
