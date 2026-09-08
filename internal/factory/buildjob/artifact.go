package buildjob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory/builder"
	"golang.org/x/sys/unix"
)

// MaxArtifactBytes includes bundle headers and pack data. Export is all or nothing.
const MaxArtifactBytes int64 = 64 << 20
const ArtifactFilename = "candidate.bundle"

type Artifact struct {
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	BaseSHA  string `json:"baseSha"`
}

var commitID = regexp.MustCompile("^[0-9a-f]{40}$")

// Use fresh metadata with exactly one trusted observed commit ref. Never load
// checkout config or enumerate its refs. Objects remain untrusted Git input.
func exportBundle(ctx context.Context, dir *os.File, request builder.Request, result builder.Result, limit int64) (*Artifact, error) {
	if !commitID.MatchString(request.BaseSHA) || !commitID.MatchString(result.CandidateSHA) || result.CandidateSHA != result.HEAD || result.CandidateSHA == request.BaseSHA || result.Branch != request.Branch || !result.Clean || result.ExitCode == nil || *result.ExitCode != 0 || result.Signal != 0 {
		return nil, errors.New("invalid candidate")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tmp, e := os.MkdirTemp("", "buildjob-git-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(tmp)
	if e = gitRun(ctx, nil, "init", "--bare", "--template=", tmp); e != nil {
		return nil, e
	}
	objects := filepath.Join(request.Workspace, ".git", "objects")
	objectDir, _, e := openDirectory(filepath.Join(objects, "unused"), false)
	if e != nil {
		return nil, e
	}
	objectDir.Close()
	if strings.ContainsAny(objects, "\n\r") {
		return nil, errors.New("unsafe object directory")
	}
	if e = os.WriteFile(filepath.Join(tmp, "objects/info/alternates"), []byte(objects+"\n"), 0600); e != nil {
		return nil, e
	}
	if e = gitRun(ctx, nil, "--git-dir="+tmp, "update-ref", "refs/heads/candidate", result.CandidateSHA); e != nil {
		return nil, e
	}
	// The trusted baseline is the only boundary. Do not copy agent-controlled
	// shallow metadata or traverse history older than the staged baseline.
	if e = os.WriteFile(filepath.Join(tmp, "shallow"), []byte(request.BaseSHA+"\n"), 0600); e != nil {
		return nil, e
	}
	if e = gitRun(ctx, nil, "--git-dir="+tmp, "merge-base", "--is-ancestor", request.BaseSHA, result.CandidateSHA); e != nil {
		return nil, errors.New("candidate does not descend from baseline")
	}
	name := ".job.bundle.tmp"
	fd, e := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	defer unix.Unlinkat(int(dir.Fd()), name, 0)
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	hash := sha256.New()
	w := &boundedWriter{dst: io.MultiWriter(f, hash), remaining: limit}
	if limit <= 0 || limit > MaxArtifactBytes {
		return nil, errors.New("invalid artifact limit")
	}
	if e = gitRun(ctx, w, "--git-dir="+tmp, "bundle", "create", "--version=2", "-", "refs/heads/candidate", "^"+request.BaseSHA); e != nil {
		return nil, errors.New("bundle export failed")
	}
	if w.exceeded || w.remaining == limit {
		return nil, errors.New("bundle exceeds limit or empty")
	}
	if e = f.Sync(); e != nil {
		return nil, e
	}
	if e = f.Close(); e != nil {
		return nil, e
	}
	if e = unix.Renameat2(int(dir.Fd()), name, int(dir.Fd()), ArtifactFilename, unix.RENAME_NOREPLACE); e != nil {
		return nil, e
	}
	if e = dir.Sync(); e != nil {
		return nil, e
	}
	return &Artifact{Filename: ArtifactFilename, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: limit - w.remaining, BaseSHA: request.BaseSHA}, nil
}

type boundedWriter struct {
	dst       io.Writer
	remaining int64
	exceeded  bool
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		w.exceeded = true
		return 0, errors.New("artifact limit")
	}
	n, e := w.dst.Write(p)
	w.remaining -= int64(n)
	return n, e
}
func gitRun(ctx context.Context, output io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)...)
	for _, v := range os.Environ() {
		name, _, _ := strings.Cut(v, "=")
		if !strings.HasPrefix(name, "GIT_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0")
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	group := prepareProcess(cmd, time.Second)
	if err := cmd.Start(); err != nil {
		return err
	}
	return group.wait(cmd)
}
