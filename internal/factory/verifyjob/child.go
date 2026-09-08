package verifyjob

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory/verification"
)

// Child consumes only a verification.Request, never the parent Job. Its exit
// describes report production; individual check outcomes remain in the report.
func Child(ctx context.Context, input io.Reader, output io.Writer) error {
	b, e := io.ReadAll(io.LimitReader(input, 200001))
	if e != nil || len(b) > 200000 {
		return fmt.Errorf("invalid child input")
	}
	var req verification.Request
	if decode(b, &req) != nil || validateRequest(req) != nil {
		return fmt.Errorf("invalid child request")
	}
	r, runErr := verification.Run(ctx, req)
	// Diagnostics remain in private manifest/log artifacts. Callback metadata is inert.
	if r.Error != "" {
		r.Error = "verification_incomplete"
	}
	for i := range r.Checks {
		if r.Checks[i].Error != "" {
			r.Checks[i].Error = "check_incomplete"
		}
	}
	b, e = json.Marshal(r)
	if e != nil || len(b) > 180000 {
		return fmt.Errorf("report exceeds limit")
	}
	if _, e = output.Write(b); e != nil {
		return fmt.Errorf("report write failed")
	}
	return runErr
}

type processResult struct {
	ExitCode  *int
	Signal    int
	Truncated bool
	Report    *verification.Report
}
type boundedOutput struct {
	bytes.Buffer
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 180000 - b.Len()
	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func runChild(ctx context.Context, req verification.Request) (r processResult, err error) {
	exe, e := os.Executable()
	if e != nil {
		return r, fmt.Errorf("child unavailable")
	}
	home, e := os.MkdirTemp(req.OutputDir, ".job.home-")
	if e != nil {
		return r, e
	}
	b, _ := json.Marshal(req)
	// Context cancellation is relayed gracefully so Run can kill/reap its check
	// groups and finish evidence. A bounded grace period handles an unresponsive child.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), exe, "--child")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "LANG=C", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(b)
	out := &boundedOutput{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	group := prepareProcess(cmd, time.Second)
	if e = cmd.Start(); e != nil {
		return r, fmt.Errorf("child launch failed")
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		signal := func(s syscall.Signal) {
			group.Lock()
			defer group.Unlock()
			if group.live {
				_ = syscall.Kill(-cmd.Process.Pid, s)
			}
		}
		signal(syscall.SIGTERM)
		timer := time.NewTimer(40 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
			signal(syscall.SIGKILL)
		}
	}()
	e = group.wait(cmd)
	close(done)
	<-stopped
	if s := cmd.ProcessState; s != nil {
		if status, ok := s.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			r.Signal = int(status.Signal())
		} else {
			code := s.ExitCode()
			r.ExitCode = &code
		}
	}
	r.Truncated = out.truncated
	var report verification.Report
	if !out.truncated && decode(out.Bytes(), &report) == nil && report.ExpectedSHA == req.ExpectedSHA {
		r.Report = &report
	} else {
		return r, fmt.Errorf("child report unavailable")
	}
	if e != nil || report.Error != "" {
		return r, fmt.Errorf("verification incomplete")
	}
	return r, nil
}

func validateRequest(req verification.Request) error {
	for _, p := range []string{req.Workspace, req.OutputDir} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" {
			return fmt.Errorf("invalid directory")
		}
		d, _, e := openDirectory(filepath.Join(p, "placeholder"), p == req.OutputDir)
		if e != nil {
			return e
		}
		d.Close()
	}
	for _, pair := range [][2]string{{req.Workspace, req.OutputDir}, {req.OutputDir, req.Workspace}} {
		rel, _ := filepath.Rel(pair[0], pair[1])
		if rel != ".." && !strings.HasPrefix(rel, "../") {
			return fmt.Errorf("workspace and attempt must be separate")
		}
	}
	sha, e := hex.DecodeString(req.ExpectedSHA)
	if e != nil || (len(sha) != 20 && len(sha) != 32) || strings.ToLower(req.ExpectedSHA) != req.ExpectedSHA {
		return fmt.Errorf("invalid candidate SHA")
	}
	if len(req.Checks) == 0 || len(req.Checks) > 64 {
		return fmt.Errorf("invalid check count")
	}
	for _, c := range req.Checks {
		if len(c.Argv) == 0 || c.Argv[0] == "" || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 3600 || c.Cwd == "" {
			return fmt.Errorf("invalid check")
		}
		for _, a := range c.Argv {
			if strings.ContainsRune(a, 0) {
				return fmt.Errorf("invalid argument")
			}
		}
		for _, part := range strings.Split(c.Cwd, "/") {
			if part == ".." {
				return fmt.Errorf("unsafe check cwd")
			}
		}
		cwd := c.Cwd
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(req.Workspace, cwd)
		}
		rel, _ := filepath.Rel(req.Workspace, cwd)
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("check outside workspace")
		}
		d, _, e := openDirectory(filepath.Join(cwd, "placeholder"), false)
		if e != nil {
			return e
		}
		d.Close()
	}
	return nil
}
