# Shared workers

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

The controller manages logical slot and directory IDs through this provider;
these IDs are never passed to Railway as service or volume IDs. Deleting a box
deletes only its directory, not the worker or permanent volume. Creating boxes,
hibernating, resuming and changing desired slots within configured capacity do
not redeploy the physical worker. Additional physical workers currently require
separate provisioning and aliases; automatic host scale-out is not implemented.

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
Blender MCP currently requires a dedicated worker because its port is global.
Retained workspaces cannot migrate to another physical worker/alias.

## Verification

Run `bash tests/shared-worker/run.sh` with Docker and Go available. Optionally set
`VMBOX_TEST_GO` and `VMBOX_TEST_SHARED_IMAGE` to local binaries/images. The script
uses and removes explicitly disposable worker and PostgreSQL containers. It checks
two simultaneous desktops/screenshots, distinct users/files/tmux state, controller
creation and runtime staging, authenticated CLI relay, hibernate/resume and deletion
without disrupting the sibling box. It does not contact production or Railway.

The broader physical-worker inventory, autoscaling, quotas and recovery roadmap
remains in [the shared-slot proposal](SHARED-WORKER-SLOTS-PLAN.md). The provider
described here is the implemented first step, not completion of that entire plan.

Production verification on 2026-09-16: Railway service `vmbox-shared-01`, controller
alias `shared-worker/shared-01`, has two healthy logical slots. Two explicitly
disposable boxes simultaneously ran separate desktops and returned real PNG
screenshots; reconnecting each reused its existing shell session. Both test boxes
were queued for deletion afterwards. Existing dedicated workers were not restarted.
