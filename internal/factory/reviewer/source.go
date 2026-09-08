package reviewer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

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

// inspect never reads the checkout's Git config or executes its hooks/filters.
// Only refs and objects are exposed to a freshly initialized Git directory.
func inspect(ctx context.Context, workspace string, baseSHA string, bundle *string) (sha string, clean bool, err error) {
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
		var out boundedBuffer
		out.limit = maxSource
		cmd.Stdout = &out
		cmd.Stderr = &out
		e := cmd.Run()
		b := out.Bytes()
		if out.truncated {
			return "", errors.New("git output exceeds limit")
		}
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
	// Discard cached stats and index flags after the staged audit above.
	if e = os.Remove(filepath.Join(tmp, "index")); e != nil && !os.IsNotExist(e) {
		return sha, false, e
	}
	if _, e = run("read-tree", sha); e != nil {
		return sha, false, e
	}
	status, e := run("status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching", "--ignore-submodules=all")
	if e != nil {
		return sha, false, e
	}
	if bundle != nil && status == "" {
		*bundle, e = sourceBundle(run, baseSHA, sha)
		if e != nil {
			return sha, false, e
		}
	}
	return sha, status == "", nil
}
