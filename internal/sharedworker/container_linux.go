package sharedworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ContainerRuntime is opt-in host-side Docker supervision. Never mount the
// Docker socket or the supervisor root into an agent container.
type ContainerRuntime struct{ Root, Image, Prefix string }

func NewContainerRuntime(root, image string) (*ContainerRuntime, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" || strings.ContainsAny(root, ",\n") {
		return nil, errors.New("container runtime requires a clean absolute data root")
	}
	if !strings.Contains(image, "@sha256:") {
		return nil, errors.New("container runtime requires a digest-pinned image")
	}
	sum := sha256.Sum256([]byte(root))
	r := &ContainerRuntime{Root: root, Image: image, Prefix: "vmbox-" + hex.EncodeToString(sum[:4])}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := r.run(ctx, "info", "--format", "{{.CgroupVersion}}")
	if err != nil || strings.TrimSpace(string(out)) != "2" {
		return nil, errors.New("container isolation requires a reachable local Docker daemon with cgroup v2")
	}
	if _, err := r.run(ctx, "image", "inspect", image); err != nil {
		return nil, errors.New("container worker image must be pulled before startup")
	}
	// Refuse container mode if the host-side boundary is absent. Docker bridge
	// separation alone does not block access to host services or private networks.
	checks := [][]string{{"iptables", "-w", "-C", "INPUT", "-i", "vb+", "-j", "REJECT"}, {"iptables", "-w", "-C", "DOCKER-USER", "-i", "vb+", "-j", "VMBOX-ISOLATED"}, {"ip6tables", "-w", "-C", "INPUT", "-i", "vb+", "-j", "REJECT"}, {"ip6tables", "-w", "-C", "FORWARD", "-i", "vb+", "-j", "REJECT"}}
	for _, subnet := range []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4"} {
		checks = append(checks, []string{"iptables", "-w", "-C", "VMBOX-ISOLATED", "-d", subnet, "-j", "REJECT"})
	}
	for _, check := range checks {
		if err := exec.CommandContext(ctx, check[0], check[1:]...).Run(); err != nil {
			return nil, errors.New("container isolation firewall is not installed; refusing to start")
		}
	}
	return r, nil
}

func (r *ContainerRuntime) name(w Workspace) string { return r.Prefix + "-" + w.ID }
func (r *ContainerRuntime) root(w Workspace) string { return filepath.Join(r.Root, "workspaces", w.ID) }
func (r *ContainerRuntime) config(w Workspace) string {
	return filepath.Join(r.Root, ".shared-worker", "containers", w.ID)
}
func (r *ContainerRuntime) bridge(w Workspace) string {
	return "vb" + strings.TrimPrefix(r.Prefix, "vmbox-")[:4] + strconv.Itoa(w.UID)
}
func (r *ContainerRuntime) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "DOCKER_HOST=unix:///var/run/docker.sock"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker %s failed: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

type containerInspect struct {
	State struct {
		Running bool
		Pid     int
	}
	Config     struct{ Labels map[string]string }
	HostConfig struct{ Memory, MemorySwap int64 }
}

func (r *ContainerRuntime) inspect(ctx context.Context, w Workspace) (*containerInspect, error) {
	out, err := r.run(ctx, "container", "ls", "-a", "--filter", "name=^/"+r.name(w)+"$", "--format", "{{.ID}}")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil, nil
	}
	out, err = r.run(ctx, "container", "inspect", r.name(w))
	if err != nil {
		return nil, err
	}
	var items []containerInspect
	if json.Unmarshal(out, &items) != nil || len(items) != 1 {
		return nil, errors.New("invalid container inspection")
	}
	if items[0].Config.Labels["io.vmbox.workspace"] != w.ID || items[0].Config.Labels["io.vmbox.root"] != r.Root || items[0].Config.Labels["io.vmbox.policy"] != r.Image+":v2-sudo" {
		return nil, errors.New("container identity or policy mismatch")
	}
	return &items[0], nil
}

func safeDirectory(path string, owner int, create bool) error {
	if create {
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || (st.Uid != 0 && st.Uid != uint32(owner)) {
		return errors.New("unsafe container workspace directory")
	}
	if err := os.Chown(path, owner, owner); err != nil {
		return err
	}
	return os.Chmod(path, 0700)
}

func (r *ContainerRuntime) Prepare(ctx context.Context, w Workspace) error {
	if err := validateWorkspace(w); err != nil {
		return err
	}
	memoryGiB, swapGiB, err := containerMemoryLimits(w)
	if err != nil {
		return err
	}
	parent := filepath.Join(r.Root, "workspaces")
	if err := os.Mkdir(parent, 0711); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || st.Uid != 0 || info.Mode().Perm()&0022 != 0 {
		return errors.New("unsafe container workspace parent")
	}
	if err := safeDirectory(r.root(w), w.UID, true); err != nil {
		return err
	}
	for _, dir := range []string{"home", "workspace", "tmp", "run", ".vmbox"} {
		if err := safeDirectory(filepath.Join(r.root(w), dir), w.UID, true); err != nil {
			return err
		}
	}
	existing, err := r.inspect(ctx, w)
	if err != nil {
		return err
	}
	if existing != nil {
		if !existing.State.Running {
			_, err = r.run(ctx, "start", r.name(w))
		}
		if err != nil {
			return err
		}
		// Explicit saved limits are authoritative across wakeups. Legacy
		// workspaces retain host-side customization except the old no-swap
		// policy, which is upgraded in place.
		explicit := w.MemoryGiB != 0 || w.SwapGiB != nil
		if (explicit && (existing.HostConfig.Memory != memoryGiB<<30 || existing.HostConfig.MemorySwap != (memoryGiB+swapGiB)<<30)) || (!explicit && existing.HostConfig.Memory == 2<<30 && existing.HostConfig.MemorySwap == 2<<30) {
			// Docker 26 requires the memory limit alongside --memory-swap even
			// when that limit is already set on the running container.
			if _, err = r.run(ctx, "update", "--memory", fmt.Sprintf("%dg", memoryGiB), "--memory-swap", fmt.Sprintf("%dg", memoryGiB+swapGiB), r.name(w)); err != nil {
				return err
			}
		}
		return r.waitReady(ctx, w)
	}
	// A separate bridge for each workspace prevents ordinary inter-container
	// routing. The host deployment also denies bridge->host/private-network traffic.
	netName := r.name(w) + "-net"
	out, err := r.run(ctx, "network", "ls", "--filter", "name=^"+netName+"$", "--format", "{{.Name}}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) == "" {
		_, err = r.run(ctx, "network", "create", "--label", "io.vmbox.workspace="+w.ID, "--label", "io.vmbox.root="+r.Root, "--opt", "com.docker.network.bridge.name="+r.bridge(w), "--opt", "com.docker.network.bridge.enable_icc=false", netName)
		if err != nil {
			return err
		}
	} else {
		out, err = r.run(ctx, "network", "inspect", "--format", "{{index .Labels \"io.vmbox.root\"}}|{{index .Labels \"io.vmbox.workspace\"}}", netName)
		if err != nil || strings.TrimSpace(string(out)) != r.Root+"|"+w.ID {
			return errors.New("container network identity mismatch")
		}
	}
	args := r.createArgs(w)
	if _, err = r.run(ctx, args...); err != nil {
		return err
	}
	_, err = r.run(ctx, "start", r.name(w))
	if err != nil {
		return err
	}
	return r.waitReady(ctx, w)
}

// Accounts and sudo policy live only in the container's writable layer. Package
// managers may update passwd/group normally; no host identity file is mounted.
const containerInit = `set -eu
uid="$1"
name="vmw$uid"
if ! getent group "$name" >/dev/null; then groupadd --gid "$uid" "$name"; fi
if ! id "$name" >/dev/null 2>&1; then useradd --uid "$uid" --gid "$uid" --home-dir /data/home --shell /bin/bash --no-create-home "$name"; fi
test "$(id -u "$name")" = "$uid"
printf '%s ALL=(ALL:ALL) NOPASSWD: ALL\n' "$name" > /etc/sudoers.d/vmbox-container
chmod 0440 /etc/sudoers.d/vmbox-container
visudo -cf /etc/sudoers.d/vmbox-container >/dev/null
touch /run/vmbox-container-ready
exec /usr/bin/tini -s -- /bin/sleep infinity
`

func (r *ContainerRuntime) waitReady(ctx context.Context, w Workspace) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := r.run(ctx, "exec", r.name(w), "test", "-f", "/run/vmbox-container-ready"); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("container account initialization did not become ready")
		case <-tick.C:
		}
	}
}

func (r *ContainerRuntime) createArgs(w Workspace) []string {
	memoryGiB, swapGiB, _ := containerMemoryLimits(w)
	args := []string{"create", "--name", r.name(w), "--hostname", "box-" + w.ID[:12], "--label", "io.vmbox.workspace=" + w.ID, "--label", "io.vmbox.root=" + r.Root, "--label", "io.vmbox.policy=" + r.Image + ":v2-sudo",
		"--network", r.name(w) + "-net", "--user", "0:0", "--cap-drop=ALL",
		"--cap-add=CHOWN", "--cap-add=DAC_OVERRIDE", "--cap-add=FOWNER", "--cap-add=FSETID", "--cap-add=SETUID", "--cap-add=SETGID", "--cap-add=SETFCAP", "--cap-add=SYS_CHROOT", "--cap-add=KILL", "--cap-add=NET_BIND_SERVICE", "--cap-add=AUDIT_WRITE",
		"--memory", fmt.Sprintf("%dg", memoryGiB), "--memory-swap", fmt.Sprintf("%dg", memoryGiB+swapGiB), "--cpus", "1", "--pids-limit", "512", "--shm-size", "256m", "--restart", "no", "--no-healthcheck",
		"--log-opt", "max-size=10m", "--log-opt", "max-file=2", "--workdir", "/",
		"--mount", "type=bind,src=" + r.root(w) + ",dst=/data,bind-propagation=rprivate",
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=256m,mode=1777", "--tmpfs", "/var/tmp:rw,nosuid,nodev,size=128m,mode=1777", "--tmpfs", "/run:rw,nosuid,nodev,size=64m,mode=1777",
		"--entrypoint", "/bin/sh", r.Image, "-c", containerInit, "vmbox-container-init", strconv.Itoa(w.UID)}
	return args
}

func containerMemoryLimits(w Workspace) (int64, int64, error) {
	memoryGiB, swapGiB := w.MemoryGiB, int64(1)
	if memoryGiB == 0 {
		memoryGiB = 2 // Legacy workspaces used the original fixed limit.
	}
	if w.SwapGiB != nil {
		swapGiB = *w.SwapGiB
	}
	if memoryGiB < 1 || memoryGiB > 8 || swapGiB < 0 || swapGiB > 4 {
		return 0, 0, errors.New("memory must be 1–8 GiB and swap 0–4 GiB")
	}
	return memoryGiB, swapGiB, nil
}

func (r *ContainerRuntime) UpdateMemory(ctx context.Context, w Workspace) error {
	memoryGiB, swapGiB, err := containerMemoryLimits(w)
	if err != nil {
		return err
	}
	current, err := r.inspect(ctx, w)
	if err != nil {
		return err
	}
	if current == nil || !current.State.Running {
		return errors.New("workspace container is not running")
	}
	_, err = r.run(ctx, "update", "--memory", fmt.Sprintf("%dg", memoryGiB), "--memory-swap", fmt.Sprintf("%dg", memoryGiB+swapGiB), r.name(w))
	return err
}

func (r *ContainerRuntime) Command(ctx context.Context, w Workspace, argv []string) (*exec.Cmd, error) {
	if err := validateWorkspace(w); err != nil {
		return nil, err
	}
	if len(argv) == 0 {
		return nil, errors.New("command required")
	}
	current, err := r.inspect(ctx, w)
	if err != nil {
		return nil, err
	}
	if current == nil || !current.State.Running {
		return nil, errors.New("workspace container is not running")
	}
	args := []string{"exec", "-i", "--user", fmt.Sprintf("%d:%d", w.UID, w.UID), "--workdir", "/data/workspace", r.name(w), "env", "-i",
		"HOME=/data/home", "USER=" + workspaceUser(w), "LOGNAME=" + workspaceUser(w), "SHELL=/bin/bash", "LANG=C.UTF-8", "TERM=xterm-256color",
		"PATH=/data/home/bin:/data/home/.local/bin:/opt/bun/bin:/opt/foundry/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"VMBOX_WORKSPACE_ROOT=/data", "VMBOX_RUNTIME_DIR=/data/.vmbox", "VMBOX_DESKTOP_DISPLAY=:" + strconv.Itoa(w.Display), "TMPDIR=/data/tmp", "TMUX_TMPDIR=/data/tmp", "XDG_RUNTIME_DIR=/data/run"}
	args = append(args, argv...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "DOCKER_HOST=unix:///var/run/docker.sock"}
	return cmd, nil
}

func (r *ContainerRuntime) Stop(ctx context.Context, w Workspace) error {
	if err := validateWorkspace(w); err != nil {
		return err
	}
	current, err := r.inspect(ctx, w)
	if err != nil {
		return err
	}
	if current != nil {
		if _, err = r.run(ctx, "rm", "-f", r.name(w)); err != nil {
			return err
		}
	}
	name := r.name(w) + "-net"
	out, err := r.run(ctx, "network", "ls", "--filter", "name=^"+name+"$", "--format", "{{.Name}}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil
	}
	out, err = r.run(ctx, "network", "inspect", "--format", "{{index .Labels \"io.vmbox.root\"}}|{{index .Labels \"io.vmbox.workspace\"}}", name)
	if err != nil || strings.TrimSpace(string(out)) != r.Root+"|"+w.ID {
		return errors.New("container network identity mismatch")
	}
	_, err = r.run(ctx, "network", "rm", name)
	return err
}
func (r *ContainerRuntime) Delete(ctx context.Context, w Workspace) error {
	if err := r.Stop(ctx, w); err != nil {
		return err
	}
	if err := os.RemoveAll(r.root(w)); err != nil {
		return err
	}
	return os.RemoveAll(r.config(w))
}
