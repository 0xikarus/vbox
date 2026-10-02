package sharedworker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestContainerCreateBoundary(t *testing.T) {
	r := &ContainerRuntime{Root: "/srv/disposable", Image: "example@sha256:abc", Prefix: "vmbox-12345678"}
	w := Workspace{ID: "0123456789abcdef0123456789abcdef", UID: 30000, Display: 1000}
	args := strings.Join(r.createArgs(w), " ")
	for _, want := range []string{"--cap-drop=ALL", "--cap-add=SETUID", "--cap-add=SETGID", "--memory 2g --memory-swap 3g", "--cpus 1", "--pids-limit 512", "NOPASSWD: ALL", "dst=/data,bind-propagation=rprivate", "--network " + r.name(w) + "-net"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing boundary %q", want)
		}
	}
	for _, bad := range []string{"docker.sock", "--privileged", "--network host", "--pid host", "--ipc host", "VMBOX_SHARED_TOKEN", "--cap-add=SYS_ADMIN", "--cap-add=NET_ADMIN", "--cap-add=SYS_PTRACE", "--read-only", "no-new-privileges"} {
		if strings.Contains(args, bad) {
			t.Fatalf("unsafe argument %q", bad)
		}
	}
	if strings.Contains(args, "src=/srv/disposable,dst=") {
		t.Fatal("supervisor root mounted")
	}
}

func TestContainerMemoryLimitsRespectSavedWorkspace(t *testing.T) {
	r := &ContainerRuntime{Root: "/srv/disposable", Image: "example@sha256:abc", Prefix: "vmbox-12345678"}
	swap := int64(0)
	w := Workspace{ID: "0123456789abcdef0123456789abcdef", UID: 30000, Display: 1000, MemoryGiB: 4, SwapGiB: &swap}
	if args := strings.Join(r.createArgs(w), " "); !strings.Contains(args, "--memory 4g --memory-swap 4g") {
		t.Fatalf("explicit no-swap limits missing: %s", args)
	}
	swap = 2
	if args := strings.Join(r.createArgs(w), " "); !strings.Contains(args, "--memory 4g --memory-swap 6g") {
		t.Fatalf("saved swap limit missing: %s", args)
	}
	w.MemoryGiB = 0
	w.SwapGiB = nil
	w.CPU = 2.5
	if args := strings.Join(r.createArgs(w), " "); !strings.Contains(args, "--memory 2g --memory-swap 3g --cpus 2.5") {
		t.Fatalf("saved CPU limit missing: %s", args)
	}
	swap = -1
	w.SwapGiB = &swap
	if _, _, err := containerMemoryLimits(w); err == nil {
		t.Fatal("negative swap limit accepted")
	}
}

func TestDisposableContainerBoundary(t *testing.T) {
	image := os.Getenv("VMBOX_TEST_CONTAINER_IMAGE")
	if image == "" {
		t.Skip("requires explicitly enabled disposable Docker tests on a host")
	}
	if os.Geteuid() != 0 {
		t.Fatal("run disposable container test on the Docker host as root")
	}
	oldMask := syscall.Umask(0077)
	defer syscall.Umask(oldMask)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".shared-worker"), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := NewContainerRuntime(root, image)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	a := Workspace{ID: NewID(), UID: 35000, Display: 6000}
	b := Workspace{ID: NewID(), UID: 35001, Display: 6001}
	for _, w := range []Workspace{a, b} {
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			if err := r.Stop(cleanup, w); err != nil {
				t.Error(err)
			}
		})
		if err := r.Prepare(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	run := func(w Workspace, script string) string {
		t.Helper()
		cmd, err := r.Command(ctx, w, []string{"sh", "-c", script})
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// The idle init must not hold the workspace busy or be killed by the
	// controller's initial prepare-hibernate probe.
	run(a, `vmbox-runtime prepare-hibernate`)
	run(a, `set -eu; test "$(sudo -n id -u)" = 0; sudo sh -c 'printf ephemeral > /usr/local/share/vmbox-sudo-test'; sudo useradd --no-create-home disposable-package-user; sudo sh -c 'test ! -e /data/workspaces; test ! -e /data/.shared-worker; test ! -e /var/run/docker.sock'; if sudo mount -t tmpfs tmpfs /mnt 2>/dev/null; then exit 1; fi`)
	run(a, `sudo apt-get update -qq && sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends hello && hello | grep -q 'Hello, world'`)
	run(a, `printf alpha > "$HOME/marker"; tmux new-session -d -s persistent 'sleep 180'; python3 -m http.server 18765 --bind 127.0.0.1 >/data/home/http.log 2>&1 &`)
	run(b, `printf beta > "$HOME/marker"; tmux new-session -d -s persistent 'sleep 180'; python3 -m http.server 18765 --bind 127.0.0.1 >/data/home/http.log 2>&1 &`)
	for _, w := range []Workspace{a, b} {
		if run(w, "id -un") != workspaceUser(w) {
			t.Fatal("workspace identity files unreadable")
		}
		run(w, `set -eu; test ! -e /data/workspaces; test ! -e /data/.shared-worker; test ! -e /var/run/docker.sock; test ! -w /usr; test -z "${VMBOX_SHARED_TOKEN:-}"; test "$(cat /sys/fs/cgroup/memory.max)" = 2147483648; test "$(cat /sys/fs/cgroup/memory.swap.max)" = 1073741824; test "$(cat /sys/fs/cgroup/pids.max)" = 512; test "$(cat /sys/fs/cgroup/cpu.max)" = '100000 100000'; test "$(tmux list-sessions -F '#{session_name}' | grep -c persistent)" = 1; curl --retry 10 --retry-connrefused --retry-delay 1 -fsS http://127.0.0.1:18765/ >/dev/null; curl -fsS --max-time 20 https://example.com >/dev/null`)
	}
	if run(a, `cat "$HOME/marker"`) != "alpha" || run(b, `cat "$HOME/marker"`) != "beta" {
		t.Fatal("workspace content crossed")
	}
	for _, name := range []string{"pid", "mnt", "net", "ipc"} {
		if run(a, "readlink /proc/self/ns/"+name) == run(b, "readlink /proc/self/ns/"+name) {
			t.Fatalf("shared %s namespace", name)
		}
	}
	// Restart preparation reuses the same environment, rather than duplicating it.
	if _, err := r.run(ctx, "update", "--memory-swap", "2g", r.name(a)); err != nil {
		t.Fatal(err)
	}
	if got := run(a, "cat /sys/fs/cgroup/memory.swap.max"); got != "0" {
		t.Fatalf("swap should be disabled before policy reconciliation, got %s", got)
	}
	if err := r.Prepare(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got := run(a, "cat /sys/fs/cgroup/memory.swap.max"); got != "1073741824" {
		t.Fatalf("swap headroom was not restored in place: %s", got)
	}
	run(a, "tmux has-session -t persistent")
	if err := r.Stop(ctx, a); err != nil {
		t.Fatal(err)
	}
	run(b, "tmux has-session -t persistent")
	if err := r.Prepare(ctx, a); err != nil {
		t.Fatal(err)
	}
	if run(a, `cat "$HOME/marker"`) != "alpha" {
		t.Fatal("persistent files lost")
	}
	run(a, `if tmux has-session -t persistent 2>/dev/null; then exit 1; fi`)
	run(a, `test ! -e /usr/local/share/vmbox-sudo-test; test "$(sudo -n id -u)" = 0`)
}
