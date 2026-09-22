# Shared workers

For host-managed, container-per-box isolation with container-local sudo, see
[Linux VPS setup](LINUX-VPS-SETUP.md) and [the container runtime](SHARED-CONTAINERS.md).
The UID and bubblewrap modes documented below remain available separately.

`shared-worker` is an opt-in provider for trusted boxes belonging to one account.
One Linux worker hosts N logical compute slots, each running at most one box.
Existing Railway and Docker providers are unchanged. This is process hosting,
not Docker-in-Docker, Railway Sandboxes, or a security sandbox.

## Deployment

Run `/usr/local/bin/vmbox-shared-worker` as root using the normal desktop image.
Attach one permanent volume at `/data`. Configure:

- `VMBOX_WORKER_MODE=shared`: select the supervisor at container startup.
- `VMBOX_SHARED_ACCOUNT_ID`: the controller account UUID.
- `VMBOX_SHARED_SLOTS`: physical worker capacity, explicitly 1–32.
- `VMBOX_SHARED_TOKEN`: a randomly generated secret of at least 32 characters.
- `PORT`: HTTP listener port, default 8080; publish through HTTPS.
- `VMBOX_SHARED_ROOT`: optional absolute data root, default `/data`.

Register a controller provider credential with provider `shared-worker`, a unique
alias for this physical worker, config `{"endpoint":"https://WORKER_HOST"}`, and
secret `{"token":"WORKER_TOKEN"}`. Set the alias's fleet desired slot count to N,
not exceeding the worker capacity. Do not register the same host under multiple
aliases. The worker must not receive the controller database or Railway token.

On startup, retained workspace identities are validated and their Unix accounts
and directories are prepared before the worker serves health or execution requests.
Conflicting identities fail startup rather than silently changing ownership.
When a viewer next observes a running box after a worker restart, the controller
verifies its workspace identity, restores its runtime/tmux state, and refreshes
the connection incarnation under the existing assignment fence. Existing live
sessions are not duplicated. Files survive restarts; terminated processes are not
resurrected, and a fresh managed agent can be started when reconnecting.

The controller manages logical slot and directory IDs through this provider;
these IDs are never passed to Railway as service or volume IDs. Deleting a box
deletes only its directory, not the worker or permanent volume. Creating boxes,
hibernating, resuming and changing desired slots within configured capacity do
not redeploy the physical worker. Additional physical workers currently require
separate provisioning and aliases; automatic host scale-out is not implemented.
New shared slot names include the controller slot UUID so the same ordinal on
different hosts cannot collide. Existing shared slot identities are preserved.

The controller's Boxes table identifies each assigned worker and slot. Capacity
shows all configured pools, groups slots by host/pool, and distinguishes physical
worker count from logical slot count. Select a Worker pool when creating a box
to compare dedicated capacity with a particular shared host without changing the
account default. Capacity edits apply to the selected pool only. For example,
two dedicated slots plus two shared aliases with two slots each are four physical
workers and six logical compute slots. Shared slots compete for host resources;
compare the same workload and check host limits, region and image versions before
drawing performance conclusions. Do not benchmark an existing live user box.

## Isolation and lifecycle

Supervisor metadata and execution journals live in root-owned
`/data/.shared-worker`. Workspace storage lives under
`/data/workspaces/<opaque-id>`. Each workspace retains its own UID, private home,
workspace, runtime state, temporary directory, tmux server and desktop display.
The supervisor starts workloads with a clean environment, no supplementary
groups, no capabilities and no-new-privileges. Workloads cannot use sudo.

Connections are fenced by worker incarnation, slot identity/revision and retained
workspace identity. Stop revokes the connection before terminating all processes
for that workspace UID. A failed stop retains the attachment; another box cannot
take it. Persistence failure stops further operations until recovery. Only one
supervisor can open a data root. A recreated slot cannot revive its old connections.

CLI connections relay through the authenticated controller; clients never receive
the supervisor token. Web terminals and desktops use the same provider stream.
Ordinary reconnects retain tmux sessions. A physical worker restart invalidates
connections and may destroy processes: do not promise live-process preservation.
Automatic recovery of occupied assignments after physical host replacement is not
yet implemented; retain the volume and reconcile/reallocate before reconnecting.

CPU, memory, PID capacity, network and disk are shared. Requested disk size is
metadata, not an enforced quota. Network ports must not collide. System packages
must be preinstalled; user-local tools work, but recipes requiring sudo do not.
Blender-tagged boxes use the same image-bundled Blender 5.1.2 and Blender MCP
1.9.1 as dedicated workers. Each shared workspace uses its stable Linux UID as
its loopback MCP port; the add-on and agent configuration agree on that port,
including when a saved scene contains another port. These ports avoid accidental
cross-box connections, not deliberate access by another trusted workspace.
Launch Blender and start the MCP server in its add-on panel as on dedicated boxes.
Older shared images without bundled Blender must be upgraded before using this
preset. Existing dedicated Blender installations are preserved.

Box creation automatically selects available capacity from the least occupied
pool (occupied slots / total slots), breaking ties by free slots and the controller
default. This is an availability heuristic, not a CPU benchmark. If all pools are
full or unavailable, creation queues in the default pool. Expand the placement
override to select a pool explicitly; existing boxes are never moved by this choice.
Retained workspaces cannot migrate to another physical worker/alias.

### Isolation tiers

Each worker selects exactly one isolation tier per box at startup and reports it.
The tier is never inferred from the deployment image.

- `uid` (default): the box runs as its own workspace UID with a clean
  environment, dropped capabilities and DAC-protected private directories. This is
  the current behavior and is **not** filesystem isolation.
- `namespace` (preferred when available): the box runs in a per-exec user, mount
  and PID namespace built with the already-shipped `bwrap`. The host root is
  mounted read-only, the box's own directory is bound at `/data`, and the host
  volume, sibling workspaces and `/data/.shared-worker` are unreachable. The
  process runs as UID 0 inside the user namespace, which maps to the workspace UID
  on the host, so host root and other UIDs are unmapped.

Control the tier with `VMBOX_SHARED_ISOLATION=uid|namespace|auto`. The default is
`uid`. `auto` uses `namespace` only when the probe confirms the primitives work;
otherwise it stays on `uid` and records a reason.

Every box reports its tier in connection metadata (`isolationTier`,
`isolationMode`, `isolationReason`) and in box labels (`isolation.tier`,
`isolation.mode`). A `uid` box is never labelled or described as isolated.

### Threat model and shared-kernel limits

All slots share one kernel, CPU, memory, network and disk. The `namespace` tier
reduces filesystem reachability between trusted boxes on one account. It is not a
defense against kernel exploits: a namespace or container escape reaches every
sibling, and shared CPU/memory/network remain shared. A `uid` box must never be
described as isolated, even when its private directories are DAC-protected.
Neither tier is a security sandbox against hostile tenants.

### What the namespace tier needs, and what Railway allows

The `namespace` tier is implemented and its sandbox is verified, but it **cannot
activate on Railway**. Probed on a live production shared worker, every namespace
primitive is refused, including as root:

```
Seccomp:  2
CapEff:   00000000800405fb      # no CAP_SYS_ADMIN (bit 21)

bwrap --unshare-user --unshare-pid ...   Creating new namespace failed: Permission denied
unshare --user                           Permission denied
unshare --pid / --mount / --ipc / --uts  Operation not permitted
```

The kernel permits namespaces (`max_user_namespaces` is large,
`unprivileged_userns_clone=1`) and `bwrap` is installed; the container runtime's
seccomp filter and the missing `CAP_SYS_ADMIN` are what block it. So on Railway
`ProbeCapabilities` reports every primitive unavailable and `SelectIsolation`
degrades every box to `uid` with a recorded reason. That is the intended,
honest behavior — but it means **shared workers on Railway provide soft isolation
only**, and the namespace tier stays inert until the host permits nested
namespaces (a privileged container, sysbox, or a VM-backed runner).

The sandbox itself is verified, on a host where namespaces are permitted, using
the argv this code actually generates:

```
own_tmp=BOX1PRIVATE                                     # the box's own tmp, not a shared one
visible_pids=5                                          # its own PID namespace
sibling_secret=/data/workspaces/box2/secret.txt: No such file or directory
sibling_dir=ls: cannot access '/data/workspaces': No such file or directory
```

Before enabling the tier on any host, run `tests/shared-worker/probe.sh` and
`tests/shared-worker/isolation.sh` there; both must pass on that host first.

### Soft isolation: what `uid` actually gives you

This is what a box on a Railway shared worker gets today. Measured on a live
worker as one box's user against a sibling box:

| | |
| --- | --- |
| read the sibling's workspace | denied (`drwx------`, distinct UIDs) |
| read `/data/.shared-worker` | denied |
| enumerate sibling workspaces | denied (`/data/workspaces` is `0711`) |
| see the sibling's processes | **visible** |
| see the whole container process table | **visible** |
| read the host root (`/etc`, `/usr`, ...) | **readable** |

Files are separated; processes and the host filesystem are not. Fixing the last
three rows requires a PID namespace or a `hidepid` remount of `/proc`, both of
which need `CAP_SYS_ADMIN`. Treat boxes on one shared worker as mutually trusted.

## Verification

Run `bash tests/shared-worker/run.sh` with Docker and Go available. Optionally set
`VMBOX_TEST_GO` and `VMBOX_TEST_SHARED_IMAGE` to local binaries/images. The script
uses and removes explicitly disposable worker and PostgreSQL containers. It checks
two simultaneous desktops/screenshots, distinct users/files/tmux state, controller
creation and runtime staging, authenticated CLI relay, hibernate/resume and deletion
without disrupting the sibling box. It does not contact production or Railway.
Set `VMBOX_TEST_SHARED_BLENDER=1` with the bundled image to additionally install
and restore the preset in both workspaces, run two real Blender GUI instances,
query their distinct scenes through MCP, and recheck the sibling after stopping
the first workspace.

The broader physical-worker inventory, autoscaling, quotas and recovery roadmap
remains in [the shared-slot proposal](SHARED-WORKER-SLOTS-PLAN.md). The provider
described here is the implemented first step, not completion of that entire plan.

Production verification on 2026-09-16: Railway service `vmbox-shared-01`, controller
alias `shared-worker/shared-01`, has two healthy logical slots. Two explicitly
disposable boxes simultaneously ran separate desktops and returned real PNG
screenshots; reconnecting each reused its existing shell session. Both test boxes
were queued for deletion afterwards. Existing dedicated workers were not restarted.
