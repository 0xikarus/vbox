# Shared workers

For host-managed, container-per-box isolation with container-local sudo, see
[Linux VPS setup](LINUX-VPS-SETUP.md) and [the container runtime](SHARED-CONTAINERS.md).
The UID and bubblewrap modes documented below remain available separately.

`shared-worker` is an opt-in provider for trusted boxes belonging to one account.
One Linux worker hosts N logical compute slots, each running at most one box.
The UID and namespace modes start processes in one worker environment; the
container mode described in the Linux VPS guide gives each box its own Docker
container. Choose the isolation tier for the workloads sharing that host.

## Deployment

For the UID and namespace modes, run `/usr/local/bin/vmbox-shared-worker` as
root using the normal desktop image. Attach one permanent volume at `/data`.
Configure:

- `VMBOX_WORKER_MODE=shared`: select the supervisor at container startup.
- `VMBOX_SHARED_ACCOUNT_ID`: the controller account UUID.
- `VMBOX_SHARED_SLOTS`: optional starting slot count, 1–32 (default 1). It only
  seeds the first start; afterwards slots are changed from the controller.
- `VMBOX_SHARED_TOKEN`: a randomly generated secret of at least 32 characters.
- `PORT`: HTTP listener port, default 8080; publish through HTTPS.
- `VMBOX_SHARED_ROOT`: optional absolute data root, default `/data`.

Register the worker from the controller's Providers page (**Add provider** →
Shared worker: its URL and token), or with provider `shared-worker`, a unique
alias for this physical worker, config `{"endpoint":"https://WORKER_HOST"}`, and
secret `{"token":"WORKER_TOKEN"}`. Do not register the same host under multiple
aliases.

## Changing slots and box size remotely

Once registered, nothing on the host needs editing. Choose **Manage** on the
pool's row on the Providers page, or use `vbox pools worker shared-worker ALIAS`:

```sh
vbox pools worker shared-worker my-vps                     # show specs and settings
vbox pools worker shared-worker my-vps --slots 5 --box-memory 3 --box-cpu 1.5
```

The worker reports its machine: CPUs, RAM, swap, disk and isolation tier.
Every value is bounded by that machine and applied live, without a restart:

- **Slots** is one number for both the worker's capacity and the controller's
  desired slots. It ranges from the boxes currently on the worker up to one slot
  per GiB of RAM (at most 32). Lowering it retires free slots first; occupied
  slots are never evicted, so hibernate or delete boxes to go lower.
- **New box size** (container isolation only) sets the default CPU (0.5 steps),
  RAM and swap for boxes created afterwards, each at most the machine's own.
  Existing boxes keep their limits; change one from its RAM and swap panel.
- Slots × default RAM may exceed the machine's RAM. That overcommit is allowed
  and shown as a warning: it is fine while boxes stay light, but under load they
  compete for memory and swap.

Settings persist in the worker's state under `/data/.shared-worker` and win over
`VMBOX_SHARED_SLOTS` on later starts (the worker logs when it ignores the
variable). Isolation mode, data root, listener, image and token remain host
configuration. A worker built before remote settings keeps its startup slot count
and the original 1 CPU, 1–8 GiB RAM, 0–4 GiB swap limits; the controller reports
that it needs an upgrade. The worker must not receive the controller database or another
provider's management credentials.

On startup, retained workspace identities are validated and their Unix accounts
and directories are prepared before the worker serves health or execution requests.
Conflicting identities fail startup rather than silently changing ownership.
When a viewer next observes a running box after a worker restart, the controller
verifies its workspace identity, restores its runtime/tmux state, and refreshes
the connection incarnation under the existing assignment fence. Existing live
sessions are not duplicated. Files survive restarts; terminated processes are not
resurrected, and a fresh managed agent can be started when reconnecting.

The controller manages logical slot and directory IDs through this provider;
they are separate from another provider's service or volume IDs. Deleting a box
deletes only its directory, not the worker or permanent volume. Creating boxes,
hibernating, resuming and changing slots or box defaults do not redeploy the
physical worker. Additional physical workers currently require
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

### Namespace tier host requirements

The `namespace` tier needs user, mount and PID namespaces, plus `bwrap`.
Installing `bwrap` alone is not enough: a host's container policy may block
namespace creation. Startup probes the actual primitives and records why `auto`
falls back to `uid`. Never claim namespace isolation from an image setting or
an untested host capability.

Before enabling the tier on a host, run `tests/shared-worker/probe.sh` and
`tests/shared-worker/isolation.sh` there. Both must pass on that host. If the
host forbids namespaces, use the container-per-box path in the
[Linux VPS guide](LINUX-VPS-SETUP.md) when stronger separation is needed.

### Soft isolation: what `uid` actually gives you

The `uid` tier relies on filesystem permissions. A typical worker has these
boundaries; verify them on the host you use:

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
without disrupting the sibling box. It does not contact a production provider.
Set `VMBOX_TEST_SHARED_BLENDER=1` with the bundled image to additionally install
and restore the preset in both workspaces, run two real Blender GUI instances,
query their distinct scenes through MCP, and recheck the sibling after stopping
the first workspace.
