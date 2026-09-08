// Package verification executes approved checks and records local evidence.
// It is a process runner, not a sandbox for hostile programs running as its user.
package verification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"golang.org/x/sys/unix"
)

const LogLimit = 1 << 20

type Request struct {
	Workspace   string
	ExpectedSHA string
	OutputDir   string
	Checks      []factory.Check
}
type Log struct {
	File      string `json:"file"`
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
	Truncated bool   `json:"truncated"`
}
type CheckReport struct {
	TimeoutSeconds int        `json:"timeoutSeconds"`
	Argv           []string   `json:"argv"`
	Cwd            string     `json:"cwd"`
	StartedAt      *time.Time `json:"start"`
	FinishedAt     *time.Time `json:"finish"`
	Executed       bool       `json:"executed"`
	ExitCode       *int       `json:"exitCode"`
	Signal         int        `json:"signal,omitempty"`
	Timeout        bool       `json:"timeout"`
	Cancel         bool       `json:"cancel"`
	Stdout         Log        `json:"stdout"`
	Stderr         Log        `json:"stderr"`
	Error          string     `json:"error,omitempty"`
}
type Report struct {
	SourceSHA      string        `json:"sourceSHA"`
	FinalSourceSHA string        `json:"finalSourceSHA"`
	ExpectedSHA    string        `json:"expectedSHA"`
	SourceClean    bool          `json:"sourceClean"`
	EvidenceDir    string        `json:"evidenceDir"`
	Checks         []CheckReport `json:"checks"`
	AllPassed      bool          `json:"allPassed"`
	Error          string        `json:"error,omitempty"`
}

// Run executes sequentially. Check failures are evidence, not Go errors. Go errors
// indicate invalid requests, source failures, cancellation, or incomplete evidence.
// OutputDir must already exist outside Workspace; a private attempt is created in it.
func Run(ctx context.Context, req Request) (report Report, err error) {
	report.ExpectedSHA = req.ExpectedSHA
	workspace, err := safeDir(req.Workspace)
	if err != nil {
		return report, err
	}
	output, err := safeDir(req.OutputDir)
	if err != nil {
		return report, err
	}
	if within(workspace, output) {
		return report, errors.New("output must be outside checkout")
	}
	if len(req.Checks) == 0 {
		return report, errors.New("at least one approved check required")
	}
	decoded, e := hex.DecodeString(req.ExpectedSHA)
	if e != nil || (len(decoded) != 20 && len(decoded) != 32) || strings.ToLower(req.ExpectedSHA) != req.ExpectedSHA {
		return report, errors.New("expected SHA must be a full lowercase object ID")
	}
	for _, c := range req.Checks {
		if len(c.Argv) == 0 || c.Argv[0] == "" || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 3600 {
			return report, errors.New("invalid approved check")
		}
		for _, a := range c.Argv {
			if strings.ContainsRune(a, 0) {
				return report, errors.New("NUL in argv")
			}
		}
		cwd, e := checkDir(workspace, c.Cwd)
		if e != nil {
			return report, e
		}
		report.Checks = append(report.Checks, CheckReport{Argv: append([]string(nil), c.Argv...), Cwd: cwd, TimeoutSeconds: c.TimeoutSeconds})
	}
	report.EvidenceDir, err = os.MkdirTemp(output, "verification-")
	if err != nil {
		return report, err
	}
	defer func() {
		err = errors.Join(err, validateEvidence(report))
		if err != nil {
			report.AllPassed = false
			report.Error = err.Error()
		}
		if e := manifest(report); e != nil {
			report.AllPassed = false
			err = errors.Join(err, e)
			report.Error = err.Error()
		}
	}()
	sha, clean, err := inspect(ctx, workspace)
	report.SourceSHA = sha
	report.FinalSourceSHA = sha
	report.SourceClean = clean
	if err != nil {
		return report, err
	}
	if sha != req.ExpectedSHA || !clean {
		return report, errors.New("initial source differs from expected clean checkout")
	}
	passed := true
	for i, c := range req.Checks {
		// Revalidate paths and source immediately before every launch.
		cwd, e := checkDir(workspace, c.Cwd)
		if e != nil {
			return report, e
		}
		sha, clean, e = inspect(ctx, workspace)
		report.FinalSourceSHA, report.SourceClean = sha, clean
		if e != nil {
			return report, e
		}
		if sha != req.ExpectedSHA || !clean {
			return report, errors.New("source changed before check")
		}
		report.Checks[i], e = execute(ctx, c, cwd, report.EvidenceDir, i)
		if e != nil {
			report.Checks[i].Error = e.Error()
		}
		r := report.Checks[i]
		passed = passed && r.Executed && r.ExitCode != nil && *r.ExitCode == 0 && r.Signal == 0 && !r.Timeout && !r.Cancel && !r.Stdout.Truncated && !r.Stderr.Truncated && r.Error == ""
		// Inspection still runs after cancellation, with its own bounded context.
		audit, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		sha, clean, auditErr := inspect(audit, workspace)
		cancel()
		report.FinalSourceSHA = sha
		report.SourceClean = clean
		if auditErr != nil || sha != req.ExpectedSHA || !clean {
			return report, errors.Join(e, auditErr, errors.New("source changed or could not be verified after check"))
		}
		if e != nil {
			return report, e
		}
	}
	report.AllPassed = passed
	return report, nil
}
func within(root, p string) bool {
	rel, e := filepath.Rel(root, p)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
func safeDir(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty directory")
	}
	for _, s := range strings.Split(filepath.ToSlash(p), "/") {
		if s == ".." {
			return "", errors.New("directory traversal rejected")
		}
	}
	abs, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	resolved, e := filepath.EvalSymlinks(abs)
	if e != nil {
		return "", e
	}
	if resolved != abs {
		return "", errors.New("symlink directory rejected")
	}
	st, e := os.Stat(abs)
	if e != nil {
		return "", e
	}
	if !st.IsDir() {
		return "", errors.New("not a directory")
	}
	return abs, nil
}
func checkDir(root, p string) (string, error) {
	if p == "" {
		return "", errors.New("empty cwd")
	}
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".." {
			return "", errors.New("cwd traversal rejected")
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	cwd, e := safeDir(p)
	if e != nil {
		return "", e
	}
	if !within(root, cwd) {
		return "", errors.New("cwd outside checkout")
	}
	return cwd, nil
}

func manifest(r Report) error {
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(r.EvidenceDir, "manifest.tmp"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(append(b, '\n'))
	e = errors.Join(e, f.Sync(), f.Close())
	if e != nil {
		return e
	}
	if e = os.Rename(filepath.Join(r.EvidenceDir, "manifest.tmp"), filepath.Join(r.EvidenceDir, "manifest.json")); e != nil {
		return e
	}
	d, e := os.Open(r.EvidenceDir)
	if e != nil {
		return e
	}
	return errors.Join(d.Sync(), d.Close())
}

func validateEvidence(r Report) error {
	if _, e := safeDir(r.EvidenceDir); e != nil {
		return e
	}
	for _, c := range r.Checks {
		if !c.Executed {
			continue
		}
		for _, l := range []Log{c.Stdout, c.Stderr} {
			p := filepath.Join(r.EvidenceDir, l.File)
			st, e := os.Lstat(p)
			if e != nil {
				return e
			}
			if !st.Mode().IsRegular() || st.Size() != l.Bytes || st.Size() > LogLimit {
				return errors.New("missing or altered essential log evidence")
			}
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != l.SHA256 {
				return errors.New("log evidence digest mismatch")
			}
		}
	}
	return nil
}

type boundedLog struct {
	f         *os.File
	n         int64
	truncated bool
	err       error
}

func (w *boundedLog) Write(p []byte) (int, error) {
	total := len(p)
	remaining := int64(LogLimit) - w.n
	if int64(len(p)) > remaining {
		w.truncated = true
		p = p[:remaining]
	}
	if len(p) > 0 && w.err == nil {
		n, e := w.f.Write(p)
		w.n += int64(n)
		w.err = e
		if n < len(p) && e == nil {
			w.err = io.ErrShortWrite
		}
	}
	return total, nil // Always drain; evidence errors must not alter process exit.
}
func (w *boundedLog) finish(name string) (Log, error) {
	e := errors.Join(w.err, w.f.Sync(), w.f.Close())
	b, re := os.ReadFile(w.f.Name())
	e = errors.Join(e, re)
	h := sha256.Sum256(b)
	return Log{File: name, SHA256: hex.EncodeToString(h[:]), Bytes: int64(len(b)), Truncated: w.truncated}, e
}
func execute(ctx context.Context, c factory.Check, cwd, dir string, i int) (r CheckReport, err error) {
	r.TimeoutSeconds = c.TimeoutSeconds
	r.Argv = append([]string(nil), c.Argv...)
	r.Cwd = cwd
	outName := fmt.Sprintf("%03d.stdout.log", i)
	errName := fmt.Sprintf("%03d.stderr.log", i)
	out, e := os.OpenFile(filepath.Join(dir, outName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return r, e
	}
	stderr, e := os.OpenFile(filepath.Join(dir, errName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		out.Close()
		return r, e
	}
	ow, ew := &boundedLog{f: out}, &boundedLog{f: stderr}
	defer func() {
		var a, b error
		r.Stdout, a = ow.finish(outName)
		r.Stderr, b = ew.finish(errName)
		err = errors.Join(err, a, b)
		if err != nil {
			r.Error = err.Error()
		}
	}()
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutSeconds)*time.Second)
	defer cancel()
	// Build tools need a writable cache, but must not inherit the worker user's
	// credential-bearing home. Each check gets a fresh private home in evidence.
	checkHome, e := os.MkdirTemp(dir, "check-home-")
	if e != nil {
		return r, e
	}
	pathEnv := runtime.GOROOT() + "/bin:/usr/local/bin:/usr/bin:/bin"
	program := c.Argv[0]
	if !strings.ContainsRune(program, '/') {
		program = ""
		for _, dir := range filepath.SplitList(pathEnv) {
			p := filepath.Join(dir, c.Argv[0])
			st, e := os.Stat(p)
			if e == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0 {
				program = p
				break
			}
		}
		if program == "" {
			return r, fmt.Errorf("executable %q not in controlled PATH", c.Argv[0])
		}
	}
	cmd := exec.CommandContext(runCtx, program, c.Argv[1:]...)
	cmd.Args[0] = c.Argv[0]
	cmd.Dir = cwd
	cmd.Env = []string{"PATH=" + pathEnv, "LANG=C", "LC_ALL=C", "HOME=" + checkHome, "XDG_CACHE_HOME=" + filepath.Join(checkHome, ".cache"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var groupMu sync.Mutex
	groupLive := true
	cmd.Cancel = func() error {
		groupMu.Lock()
		defer groupMu.Unlock()
		if !groupLive {
			return os.ErrProcessDone
		}
		e := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if e == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return e
	}
	cmd.WaitDelay = time.Second
	cmd.Stdout = ow
	cmd.Stderr = ew
	if e = cmd.Start(); e != nil {
		return r, e
	}
	now := time.Now().UTC()
	r.StartedAt = &now
	r.Executed = true
	// Observe exit without reaping the leader. Its PID pins the process group
	// identity until cancellation is disabled and descendant cleanup completes.
	// Killing a group after Wait reaped its leader could race PID reuse.
	var info unix.Siginfo
	for {
		e = unix.Waitid(unix.P_PID, cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if e != syscall.EINTR {
			break
		}
	}
	groupMu.Lock()
	if e == nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	groupLive = false
	groupMu.Unlock()
	observeErr := e
	e = cmd.Wait()
	done := time.Now().UTC()
	r.FinishedAt = &done
	if s := cmd.ProcessState; s != nil {
		if status, ok := s.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			r.Signal = int(status.Signal())
		} else {
			code := s.ExitCode()
			r.ExitCode = &code
		}
	}
	r.Cancel = ctx.Err() != nil
	r.Timeout = !r.Cancel && runCtx.Err() == context.DeadlineExceeded
	if observeErr != nil {
		return r, fmt.Errorf("observe owned process: %w", observeErr)
	}
	if r.Cancel || r.Timeout {
		return r, runCtx.Err()
	}
	var exitErr *exec.ExitError
	if e != nil && !errors.As(e, &exitErr) {
		return r, e
	}
	return r, nil
}

// inspect never reads the checkout's Git config or executes its hooks/filters.
// Only refs and objects are exposed to a freshly initialized Git directory.
func inspect(ctx context.Context, workspace string) (sha string, clean bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	gitdir, e := safeDir(filepath.Join(workspace, ".git"))
	if e != nil {
		return "", false, e
	}
	tmp, e := os.MkdirTemp("", "verification-git-")
	if e != nil {
		return "", false, e
	}
	defer os.RemoveAll(tmp)
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "/usr/bin/git", args...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + tmp, "LANG=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0"}
		b, e := cmd.CombinedOutput()
		if e != nil {
			return "", fmt.Errorf("git inspection: %w: %.1000s", e, b)
		}
		return string(b), nil
	}
	if _, e = git("init", "--bare", "--template=", tmp); e != nil {
		return "", false, e
	}
	for _, name := range []string{"HEAD", "packed-refs", "refs", "index"} {
		src := filepath.Join(gitdir, name)
		e = filepath.WalkDir(src, func(p string, d os.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) && p == src && name != "HEAD" {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("symlink Git metadata rejected")
			}
			rel, _ := filepath.Rel(gitdir, p)
			dst := filepath.Join(tmp, rel)
			if d.IsDir() {
				return os.MkdirAll(dst, 0700)
			}
			if !d.Type().IsRegular() {
				return errors.New("unsafe Git metadata")
			}
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			return os.WriteFile(dst, b, 0600)
		})
		if e != nil {
			return "", false, e
		}
	}
	objects, e := safeDir(filepath.Join(gitdir, "objects"))
	if e != nil {
		return "", false, e
	}
	if strings.ContainsAny(objects, "\n\r") {
		return "", false, errors.New("unsafe objects path")
	}
	if e = os.WriteFile(filepath.Join(tmp, "objects/info/alternates"), []byte(objects+"\n"), 0600); e != nil {
		return "", false, e
	}
	base := []string{"--git-dir=" + tmp, "--work-tree=" + workspace, "-c", "core.bare=false", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null"}
	run := func(args ...string) (string, error) { return git(append(append([]string{}, base...), args...)...) }
	head, e := run("rev-parse", "--verify", "HEAD^{commit}")
	if e != nil {
		return "", false, e
	}
	sha = strings.TrimSpace(head)
	// Reject gitlinks: auditing a nested repository would otherwise load its config.
	tree, e := run("ls-tree", "-r", sha)
	if e != nil {
		return sha, false, e
	}
	for _, line := range strings.Split(tree, "\n") {
		if strings.HasPrefix(line, "160000 ") {
			return sha, false, errors.New("submodule checkouts are not supported")
		}
	}
	staged, e := run("diff", "--cached", "--no-ext-diff", "--no-textconv", "--ignore-submodules=all", "--raw", sha, "--")
	if e != nil {
		return sha, false, e
	}
	if staged != "" {
		return sha, false, nil
	}
	// Info attributes have highest precedence. Compare raw source bytes rather
	// than permitting repository attributes to normalize away a modification.
	if e = os.MkdirAll(filepath.Join(tmp, "info"), 0700); e != nil {
		return sha, false, e
	}
	if e = os.WriteFile(filepath.Join(tmp, "info/attributes"), []byte("* -text -filter -ident -working-tree-encoding\n"), 0600); e != nil {
		return sha, false, e
	}
	if _, e = run("read-tree", sha); e != nil {
		return sha, false, e
	}
	status, e := run("status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching", "--ignore-submodules=all")
	if e != nil {
		return sha, false, e
	}
	return sha, status == "", nil
}
