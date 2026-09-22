# Host-managed shared-worker containers

`VMBOX_SHARED_ISOLATION=container` is an opt-in Linux Docker-host runtime.
The supervisor runs on the host as root; agents start as their workspace user
in separate containers and have passwordless sudo inside their own container.
Do not place the Docker socket in an agent container.

Set `VMBOX_SHARED_CONTAINER_IMAGE` to a locally pulled digest-pinned normal
desktop worker image, `VMBOX_SHARED_ROOT` to an absolute host data directory,
and the usual account, token and slot variables. Bind the supervisor to loopback
with `VMBOX_SHARED_BIND=127.0.0.1` behind an authenticated HTTPS proxy.
Install `scripts/shared-container-firewall.sh` as a boot service after Docker
and before the supervisor. Startup refuses a missing firewall or cgroup v2.
The firewall uses the `vb` bridge prefix reserved for these containers.

Each workspace has a persistent container environment shared by subsequent
execs, its own PID/mount/IPC/network namespaces and bridge, and only its own
directory mounted at `/data`. The system filesystem is writable so sudo can
install packages, create service users, and update normal system files. Accounts
and sudo policy live inside the container, not in host-mounted identity files.
Only a limited capability set needed for ordinary package installation is added;
SYS_ADMIN, NET_ADMIN, SYS_PTRACE, host namespaces and privileged mode are not
enabled. Docker's default seccomp/AppArmor restrictions remain active. This
deployment does not use user-namespace UID remapping. There are no published
agent ports. Controller streams enter
through authenticated supervisor exec, so VNC and MCP operate on the correct
private localhost. IPv4 internet/DNS is available; host services, sibling
networks, private/metadata addresses and IPv6 egress are denied.

Initial fixed limits per container are 2 GiB RAM (no additional swap), 1 CPU,
512 processes, and 256 MiB shared memory. These are reported in connection
metadata and enforced over all descendants by Docker/cgroup v2. They are not
yet configurable per box or exposed in a dedicated UI. Disk space is shared
without quotas. Reserve host/supervisor capacity when choosing slot counts.

Stop removes the container and private network, retaining workspace files.
System packages and changes outside `/data` are deliberately discarded on
hibernation/container replacement; they are not backed up or restored.
Resume recreates the environment with the same files/UID. Supervisor restart
reuses running containers and creates missing ones for active assignments;
inactive workspaces stay inactive. Existing UID-mode workspaces are not migrated
automatically: moving their stored paths and active processes needs a planned
rollout. An image/policy mismatch fails closed rather than replacing a running
container silently.

Verification on an explicitly disposable Docker host with the firewall installed:

    VMBOX_TEST_CONTAINER_IMAGE=IMAGE@sha256:DIGEST go test ./internal/sharedworker -run TestDisposableContainerBoundary -v
    VMBOX_TEST_CONTAINER_IMAGE=IMAGE@sha256:DIGEST bash tests/shared-worker/container.sh

Containers still share the host kernel and physical disk. This is not a boundary
against kernel vulnerabilities. Full noisy-neighbor stress testing, resource
event UI, configurable budgets, broader hosting-class support and cancellation
of arbitrary Docker exec descendants remain follow-up work for #238. Do not
claim all acceptance criteria of #122–#124/#238 are complete from these tests.
