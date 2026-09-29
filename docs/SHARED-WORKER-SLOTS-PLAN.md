# One worker, N compute slots

Status: broader implementation roadmap. The initial opt-in provider is documented
in [Shared workers](SHARED-WORKERS.md); not all phases below are implemented. Shared workers are
explicitly trusted-account process hosting, not Docker or Railway Sandboxes.
This relaxes the namespace-isolation requirement in SHARED-WORKER-PROVIDER.md for
this opt-in mode only: the default remains uid-based, but a namespace tier is now
available behind explicit configuration and must be verified per host before use.
Dedicated Railway boxes retain their existing contract.

## Outcome and terminology

A physical worker is one Railway service with one permanently attached volume.
It owns N logical compute slots; each slot admits one active box. A worker may
retain more than N sleeping box directories. Creating or resuming a box on an
existing worker does not deploy a service or attach a volume. Provisioning another
physical worker still uses Railway APIs and takes normal provisioning time.

Initially workers belong to one controller account and provider alias. Default N
to 1; require explicit shared-mode configuration. Configure slots per worker,
maximum workers, minimum warm workers and resource headroom. Do not promise hard
per-box resource limits until enforcement has been verified on the target runtime.

## Current coupling to remove

- schema.sql: compute_slots currently owns service identity and has a unique
  service index; logical_boxes has UNIQUE(slot_id) and unique provider volume IDs.
  Keep one box per logical slot, but move physical service ownership to workers.
- fleet_create.go and fleet_lifecycle.go attach/detach provider volumes and sanitize
  an entire slot. These operations must never run against a shared sibling box.
- direct_workers currently has one enrollment per slot. workeragent/assignment.go
  persists one binding. Shared workers need worker enrollment plus many independently
  fenced box bindings, not repeated replacement of one binding.
- boxruntime and the worker entrypoint assume /data/home, /data/workspace, a common workload
  user, and single desktop/tmux context. Shared mode requires explicit box context.

## Phase 1: data model and compatibility

Add physical workers with account, provider alias, service/deployment identity,
region, mode, health, desired capacity, volume identity and draining state. Link
compute_slots to worker_id with a unique (worker_id, ordinal). Keep slot leases
and generations per slot. Worker health and slot occupancy are different fields.

Represent workspace storage separately from compute assignment: a shared box owns
a worker/storage-domain ID and an opaque directory ID; a dedicated box owns its
existing provider volume. Multiple boxes may share the physical volume, but never
the same directory. Do not reuse volume_id with fabricated provider resource IDs.
Use mode-specific constraints instead of removing storage ownership constraints.

Backfill existing slots as dedicated workers with capacity 1, preserving IDs,
assignments and volume ownership. Existing direct-worker enrollments continue via
a compatibility adapter. Introduce schema support before enabling shared runtime;
never convert a live dedicated worker automatically. Rollback disables new shared
allocation while retaining records and files; older controllers must reject an
incompatible schema rather than misinterpret shared storage.

## Phase 2: worker supervisor and box runtime

Provision a separate shared-worker image/mode and permanent volume once. Supervisor
state is root-owned; each box gets a stable distinct UID and private directory:

    /data/boxes/<opaque-box-id>/{home,workspace,state}

Resolve paths from trusted supervisor metadata, not request-supplied paths. Persist
UID ownership and per-box generations across restart. Launch commands with a clean
environment as that user, separate HOME/workdir, tmux socket, temporary directory,
desktop display and ports. Audit native sessions, task journals, tools, profiles,
browser data, file transfer and desktop secret helpers for fixed/global paths.

Remove unrestricted workload sudo in shared mode. Preinstall system packages in
the worker image; allow user-local tooling. Existing setup recipes needing sudo
must fail clearly as unsupported, not silently modify all boxes. Shared networking,
system packages and kernel are accepted limitations, not a security sandbox.

Extend workerprotocol requests and observation messages to identify worker
incarnation, box ID, slot ID and assignment generation. Authorize each operation
against its box binding. Rebinding or disconnecting A must not revoke B's streams.
Controller reconnect reconciles bindings and results without replaying commands.

Implement durable, idempotent create/start/stop/delete operations. Track all box
processes including detached children; verify actual termination before releasing
a slot or deleting files. If reliable process cleanup cannot be established on
Railway, quarantine that slot rather than report successful hibernation. Never
kill a worker-wide tmux server or sanitize the entire shared volume.

## Phase 3: scheduling and lifecycle

Extend fleet_store.go, fleet_queue_store.go, fleet_create_store.go and reconciliation
to reserve a healthy, free logical slot transactionally, using existing leases and
ownership fences. New boxes select a compatible worker by account, alias, region,
image and resource headroom. Resuming a retained box selects only its storage owner.

- Create: reserve slot and directory identity; initialize user/files/profiles; bind
  runtime; publish running only after readiness. Recover partial creation safely.
- Hibernate: stop that box's full process tree, revoke streams, release its logical
  slot, retain directory and storage ownership. Sibling sessions keep running.
- Resume: reserve a slot on the same physical worker and restore the box context.
  Wait if that worker is full/offline; do not start against an empty directory elsewhere.
- Delete: stop and fence the box, then remove only its verified directory and UID
  mapping. One-shot output must be archived before deletion as it is today.
- Worker restart: retain files and report lost processes; reconcile all boxes.
  Supervisor restart alone should adopt verifiable surviving sessions.
- Worker replacement: require exclusive attachment of the original volume before
  recovery. Never declare data recovered just because replacement compute is ready.

An N change adds slots without redeployment. Reducing N drains excess slots and
waits for active boxes; it must not evict processes. N is admission capacity, not
additional RAM/CPU. All boxes compete for the worker's actual resources.

## Phase 4: bounded automatic worker growth

In fleet_reconciler.go, account for queued demand, free logical slots and workers
already provisioning. Provision only when compatible existing capacity cannot
serve new requests, subject to max-workers and resource settings. Serialize growth
decisions with a database lease; persist creation intent/idempotency identity before
provider calls. On ambiguous failures, discover/adopt the intended service rather
than create duplicates. Retry with backoff and expose pending/failed capacity.

Use minimum warm workers or a free-slot target to hide cold provisioning latency.
Pinned resumes on a full worker cannot be solved by provisioning another worker;
keep those requests queued and explain why. Do not grow endlessly for pinned demand.
Initial scale-down removes only drained workers with no active OR retained boxes,
after a cooldown and ownership checks. Respect warm minimums. Migration of retained
directories, backup/restore automation and compaction are separate future work.

## Phase 5: API, UI and rollout

Expose physical worker count, total/free/reserved logical slots, per-worker
occupancy, retained boxes, health and provisioning state. Version or explicitly
clarify fleet capacity commands: existing slots-set must not silently change its
meaning for dedicated providers. Add separate shared worker capacity settings and
queue reasons. Terminal, desktop and grid continue to address logical box IDs.

Implement in reviewable increments: schema compatibility; supervisor/runtime;
fixed-size shared pool; lifecycle/recovery; autoscaling; capacity UI. Enable only
on new, explicitly disposable workers during acceptance. Existing boxes remain
dedicated unless a separate migration is authorized. No production restart,
deployment or volume migration is authorized by this plan.

## Verification and release gates

Use disposable PostgreSQL plus explicitly named disposable Railway resources.
Tests must cover concurrent allocation beyond N without oversubscription; N+1
queueing; bounded autoscaling with multiple controllers and ambiguous provider
results; pinned wakeups; slot reduction; stale bindings and cross-account denial.

Run at least three boxes on one real worker. Verify separate homes, credentials,
tmux sessions and desktops; reboot persistence; detached-child cleanup; independent
hibernate/delete; controller reconnect; worker replacement with retained storage;
and preservation of one-shot results. Confirm box create/wake/delete on an existing
worker performs no Railway deploy, volume attach or service delete calls.

Measure create-to-shell and create-to-desktop separately, including login validation
and optional tool installation. Record warm and cold results; no startup-time claim
is a release guarantee before measurements. Run dedicated-provider regressions,
Go tests/vet and browser checks. Document skipped database/live checks explicitly.
