package builder

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Keep normal agent HOME/config/auth, but remove known publication and control
// namespaces. Arbitrarily named secrets and credentials on disk remain caller-owned.
func blockedEnvironment(name string) bool {
	for _, prefix := range []string{"GIT_", "VMBOX_", "CONTROLLER_", "FACTORY_", "RAILWAY_", "GH_", "GITHUB_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func trustedGit(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)...)
	cmd.Env = gitEnvironment()
	var b limitedText
	cmd.Stdout = &b
	cmd.Stderr = io.Discard
	group := prepareProcess(cmd, time.Second)
	if e := cmd.Start(); e != nil {
		return "", e
	}
	e := group.wait(cmd)
	if b.truncated {
		e = errors.New("Git output exceeds limit")
	}
	return strings.TrimSpace(b.String()), e
}

// Baseline already equals the requested commit. Change only refs, without a
// checkout that could execute local smudge filters. No existing branch replaced.
func createBranch(ctx context.Context, workspace, branch, sha string) error {
	base := []string{"--git-dir=" + filepath.Join(workspace, ".git")}
	if _, e := trustedGit(ctx, append(base, "update-ref", "refs/heads/"+branch, sha, strings.Repeat("0", len(sha)))...); e != nil {
		return e
	}
	_, e := trustedGit(ctx, append(base, "symbolic-ref", "HEAD", "refs/heads/"+branch)...)
	return e
}

// Inspect refs/index and raw worktree bytes through fresh metadata. Never load
// checkout config (including includes), hooks, filters, or textconv drivers.
func inspect(ctx context.Context, workspace string, args ...string) (string, error) {
	gitdir := filepath.Join(workspace, ".git")
	d, e := openPath(gitdir, true)
	if e != nil {
		return "", e
	}
	d.Close()
	tmp, e := os.MkdirTemp("", "builder-git-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(tmp)
	if _, e = trustedGit(ctx, "init", "--bare", "--template=", tmp); e != nil {
		return "", e
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
			return "", e
		}
	}
	objects := filepath.Join(gitdir, "objects")
	objectDir, e := openPath(objects, true)
	if e != nil {
		return "", e
	}
	objectDir.Close()
	if strings.ContainsAny(objects, "\n\r") {
		return "", errors.New("unsafe objects path")
	}
	if e = os.WriteFile(filepath.Join(tmp, "objects/info/alternates"), []byte(objects+"\n"), 0600); e != nil {
		return "", e
	}

	if e = os.MkdirAll(filepath.Join(tmp, "info"), 0700); e != nil {
		return "", e
	}
	if e = os.WriteFile(filepath.Join(tmp, "info/attributes"), []byte("* -text -filter -ident -working-tree-encoding\n"), 0600); e != nil {
		return "", e
	}
	base := []string{"--git-dir=" + tmp, "--work-tree=" + workspace, "-c", "core.bare=false"}
	// Submodule inspection can load nested local config: reject gitlinks.
	tree, e := trustedGit(ctx, append(base, "ls-files", "--stage")...)
	if e != nil {
		return "", e
	}
	for _, line := range strings.Split(tree, "\n") {
		if strings.HasPrefix(line, "160000 ") {
			return "", errors.New("submodules are not supported")
		}
	}
	if len(args) > 0 && args[0] == "status" {
		staged, e := trustedGit(ctx, append(base, "diff", "--cached", "--raw", "--no-ext-diff", "--no-textconv", "--ignore-submodules=all", "HEAD", "--")...)
		if e != nil || staged != "" {
			return staged, e
		}
		// Discard stat-cache and assume-unchanged/skip-worktree flags before checking
		// raw bytes. Keep the copied index above for the staged-difference check.
		if e := os.Remove(filepath.Join(tmp, "index")); e != nil && !os.IsNotExist(e) {
			return "", e
		}
		if _, e := trustedGit(ctx, append(base, "read-tree", "HEAD")...); e != nil {
			return "", e
		}
		tree, e := trustedGit(ctx, append(base, "ls-files", "--stage")...)
		if e != nil {
			return "", e
		}
		for _, line := range strings.Split(tree, "\n") {
			if strings.HasPrefix(line, "160000 ") {
				return "", errors.New("submodules are not supported")
			}
		}
	}
	return trustedGit(ctx, append(base, args...)...)
}
