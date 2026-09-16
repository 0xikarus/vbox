package sharedworker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type LinuxRuntime struct {
	Root   string
	Binary string
}

func validateWorkspace(workspace Workspace) error {
	if len(workspace.ID) != 32 || strings.Trim(workspace.ID, "0123456789abcdef") != "" || workspace.UID < 30000 || workspace.UID >= 59000 || workspace.Display != workspace.UID-29000 {
		return errors.New("invalid workspace identity")
	}
	return nil
}

func (r *LinuxRuntime) workspaceRoot(workspace Workspace) string {
	return filepath.Join(r.Root, "workspaces", workspace.ID)
}

func workspaceUser(workspace Workspace) string { return "vmw" + strconv.Itoa(workspace.UID) }

func (r *LinuxRuntime) Prepare(ctx context.Context, workspace Workspace) error {
	if err := validateWorkspace(workspace); err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("shared worker supervisor requires root")
	}
	parent := filepath.Join(r.Root, "workspaces")
	if err := os.MkdirAll(parent, 0711); err != nil {
		return err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 {
		return errors.New("unsafe workspace parent")
	}
	root := r.workspaceRoot(workspace)
	uid := strconv.Itoa(workspace.UID)
	name := workspaceUser(workspace)
	account, err := user.LookupId(uid)
	if err != nil {
		var unknown user.UnknownUserIdError
		if !errors.As(err, &unknown) {
			return err
		}
		command := exec.CommandContext(ctx, "useradd", "--uid", uid, "--user-group", "--no-create-home", "--home-dir", filepath.Join(root, "home"), "--shell", "/bin/bash", name)
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("create workspace user: %w: %s", err, output)
		}
		account, err = user.LookupId(uid)
		if err != nil {
			return err
		}
	}
	if account.Username != name || account.HomeDir != filepath.Join(root, "home") {
		return errors.New("workspace UID belongs to another user")
	}
	groupID, err := strconv.Atoi(account.Gid)
	if err != nil {
		return err
	}
	if err := os.Mkdir(root, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err = os.Lstat(root)
	if err != nil {
		return err
	}
	stat, ok = info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || (stat.Uid != 0 && stat.Uid != uint32(workspace.UID)) {
		return errors.New("unsafe workspace directory")
	}
	if err := os.Chown(root, workspace.UID, groupID); err != nil {
		return err
	}
	if err := os.Chmod(root, 0700); err != nil {
		return err
	}
	command, err := r.Command(ctx, workspace, []string{"sh", "-c", `set -eu; umask 077; mkdir -p "$HOME/bin" "$HOME/.local/bin" "$VMBOX_WORKSPACE_ROOT/workspace" "$TMPDIR" "$XDG_RUNTIME_DIR"; if [ ! -e "$HOME/bin/vmbox-runtime" ]; then ln -s "$1" "$HOME/bin/vmbox-runtime"; fi`, "vmbox-shared-prepare", r.Binary})
	if err != nil {
		return err
	}
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("prepare workspace: %w: %s", err, output)
	}
	return nil
}

func (r *LinuxRuntime) Command(ctx context.Context, workspace Workspace, argv []string) (*exec.Cmd, error) {
	if err := validateWorkspace(workspace); err != nil {
		return nil, err
	}
	if len(argv) == 0 {
		return nil, errors.New("command required")
	}
	account, err := user.LookupId(strconv.Itoa(workspace.UID))
	if err != nil {
		return nil, err
	}
	root := r.workspaceRoot(workspace)
	home := filepath.Join(root, "home")
	if account.Username != workspaceUser(workspace) || account.HomeDir != home {
		return nil, errors.New("workspace UID identity changed")
	}
	args := []string{"--reuid", account.Uid, "--regid", account.Gid, "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", "env", "-i",
		"HOME=" + home, "USER=" + account.Username, "LOGNAME=" + account.Username, "SHELL=/bin/bash", "LANG=C.UTF-8", "TERM=xterm-256color",
		"PATH=" + home + "/bin:" + home + "/.local/bin:/opt/bun/bin:/opt/foundry/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"VMBOX_WORKSPACE_ROOT=" + root, "VMBOX_RUNTIME_DIR=" + filepath.Join(root, ".vmbox"), "VMBOX_DESKTOP_DISPLAY=:" + strconv.Itoa(workspace.Display),
		"TMPDIR=" + filepath.Join(root, "tmp"), "TMUX_TMPDIR=" + filepath.Join(root, "tmp"), "XDG_RUNTIME_DIR=" + filepath.Join(root, "run"),
	}
	args = append(args, argv...)
	command := exec.CommandContext(ctx, "setpriv", args...)
	command.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	command.Dir = filepath.Join(root, "workspace")
	return command, nil
}

func (r *LinuxRuntime) Stop(ctx context.Context, workspace Workspace) error {
	if err := validateWorkspace(workspace); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return err
		}
		found := false
		for _, entry := range entries {
			pid, err := strconv.Atoi(entry.Name())
			if err != nil || pid <= 1 {
				continue
			}
			status, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "status"))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			owned, zombie := false, false
			for _, line := range strings.Split(string(status), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == "State:" && fields[1] == "Z" {
					zombie = true
				}
				if len(fields) == 5 && fields[0] == "Uid:" {
					for _, field := range fields[1:] {
						if field == strconv.Itoa(workspace.UID) {
							owned = true
						}
					}
				}
			}
			if !owned || zombie {
				continue
			}
			found = true
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				return err
			}
		}
		if !found {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("workspace processes did not stop")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (r *LinuxRuntime) Delete(ctx context.Context, workspace Workspace) error {
	if err := validateWorkspace(workspace); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.RemoveAll(r.workspaceRoot(workspace))
}
