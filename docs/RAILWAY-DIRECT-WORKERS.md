# Direct connections to Railway workers

Implementation plan, 2026-09-11. No measurement phase is required.

## Decision and scope

Keep all resources on Railway and preserve the current `railway` provider,
logical boxes, reusable compute slots and workspace volumes. Add an authenticated
agent connection from each worker to the controller. Use it for execution,
observations, terminals, desktops, file transfers and runtime setup. Railway's
management API remains responsible for infrastructure changes only.

This plan is the selected Railway-only direction. The external Incus architecture
in RAILWAY-INDEPENDENCE.md is a separate proposal, not a dependency of this work.
No nested containers, Bubblewrap, QEMU, new provider selection or workspace
migration is required. A permanent service per logical box is also not required.

## Delivery order

1. **Direct access first:** ship the agent, enrollment, assignment fencing and
   controller transport together. Move shell, tasks, desktop and file streams to
   that transport. Prove that an enrolled running box remains usable when Railway
   API and SSH calls fail.
2. **Remove background API pressure:** use agent observations for running-box
   status, share infrastructure snapshots, and put management HTTP requests behind
   one persistent credential-scoped budget. Add webhook hints and deferred work.
3. **Migrate safely:** validate on disposable Railway resources, deploy additive
   controller support, then install agents beside existing worker processes.
   Switch each worker only after its assignment and surviving sessions verify.

These are delivery milestones, not a measurement phase. Existing Railway resource
boundaries remain intact; the agent changes connectivity, not sandbox isolation.
New allocations and volume changes still wait for Railway when its API is limited.

```text
Browser / CLI -- HTTPS + WebSocket --> Controller + PostgreSQL on Railway
                                           ^
                                           | authenticated outbound WebSocket
                                      worker agent
                                           |
                                   vmbox-runtime / tmux / desktop

Controller -- budgeted management API --> Railway services, volumes, deployments
```

For an enrolled, running box, attaching or reconnecting must not call Railway's
API or Railway SSH. This includes indirect calls hidden in assignment validation,
runtime enablement, session lookup, image inspection and health checks.
An offline agent produces an explicit unavailable/reconnecting state, not an
automatic Railway SSH fallback. A sleeping box still needs Railway allocation,
volume attachment and deployment before its agent can connect.

## 1. Add the worker connection protocol

Create `cmd/vmbox-worker-agent` and a shared `internal/workerprotocol` package.
The agent runs beside existing runtime processes and opens a TLS WebSocket to a
controller endpoint. Start it from the worker image/entrypoint under a supervisor
that can restart the agent without terminating tmux or desktop processes.

The current hostd HTTP endpoints/heartbeat do not supply this protocol. Keep the
external-host implementation separate rather than routing this through an Incus
or Docker provider.

- Versioned handshake: enrolled worker identity, runtime capabilities, fresh
  agent incarnation, current assignment generation and workspace marker.
- Initial observation snapshot, immediate state-change events and a heartbeat
  every 20 seconds. After three missed heartbeats, mark connectivity unknown;
  do not infer that compute stopped or a task completed.
- Multiplex control requests, exact-argv execution, stdin/stdout/stderr, PTY
  resize, desktop byte streams and bounded file transfers. Use explicit stream
  IDs, per-stream flow control, bounded buffers and reserved control capacity so
  desktop output cannot starve heartbeats or commands.
- Return real exit codes independently of transport closure. Never rerun a task
  or replay terminal input after an ambiguous disconnect.
- Back off agent reconnects with jitter. Reconnection sends a fresh snapshot
  and reconciles outstanding operation IDs and results.

## 2. Persist identities and enforce assignment fencing

Add additive schema migrations for enrolled workers, credential hashes, agent
incarnations, connection ownership leases, observations and operation records.
Keep box/service identities and existing Railway metadata intact.

Enrollment uses an expiring one-time credential scoped to the account and exact
compute slot, provisioned through the controller's trusted bootstrap path. Exchange
it for a revocable worker credential; keep only its hash on the controller. Never
trust an agent's self-reported service/account ID as enrollment authority.

Worker credentials grant access only to that slot's agent protocol. They must not
grant controller administration, other-box access or Railway API access. Keep them
out of workspace files, logs, command arguments and browser/CLI responses. The
current box user has sudo: do not claim the agent credential is protected from a
malicious root user in that same worker. Limit its authority accordingly.

Every command and stream binds account, box, slot, assignment generation and
agent incarnation. The controller validates its database assignment and the agent
validates its local binding. Reassignment invalidates old streams and tickets.
An agent restart gets a new incarnation but must discover surviving runtime
sessions; a socket reconnect to the same agent retains the incarnation.

Fence duplicate connections with a database-backed ownership epoch/lease. An old
connection cannot remain authoritative when a new controller or agent takes over.
Use the existing single controller process for the first rollout; enforce that
deployment constraint explicitly. Multiple controller replicas require routing
requests to the connection owner before horizontal scaling is enabled.

## 3. Route all running-box operations over the agent

Implement a worker transport satisfying `ConnectionExecutor` and
`ConnectionStreamer`, with provider-neutral connection revisions. Update the
connection resolver and assignment validation to use the enrolled worker's live
identity instead of querying Railway deployment instances on each operation.
Retain legacy deployment-instance fencing for workers not yet migrated.

Reuse `vmbox-runtime` for task execution, sessions, terminal and desktop handling.
Carry bootstrap payloads, selected login profiles and tools over bounded private
streams, preserving existing limits and ownership checks.

Update these paths together:

- `internal/controller/fleet_lifecycle.go`: validate assignment using the database
  and authenticated agent; keep provider storage checks at lifecycle boundaries.
- `internal/controller/web_terminal.go` and related workspace/Grid handlers:
  stream through the agent while preserving authorization, origin checks, stream
  limits and viewer-only disconnect semantics.
- `internal/transport` and CLI connection handling: add controller WebSocket
  transport for shell, task, transfer and desktop connections, including local
  VNC forwarding. Clients need controller authentication, not Railway credentials.
- Process, interaction, runtime setup and session reconcilers: consume agent
  observations and execution results rather than using Railway SSH.

Do not hold a database transaction for an interactive stream's lifetime. Authorize
and bind the stream, then enforce revocation and assignment changes separately.

## 4. Separate worker health from infrastructure reconciliation

Use local controller observations for box lists, status, Grid, task progress and
session discovery. Display observation age/connectivity honestly. Page refreshes
must not independently fan out into provider reads.

Replace repeated project-wide inventory reads with one shared snapshot per
project/environment and provider credential revision. Coalesce concurrent reads.
Refresh healthy infrastructure in the background about every five minutes, with
jitter, and request targeted refreshes after known infrastructure changes.

Keep infrastructure observations distinct from worker assertions: agents cannot
authorize volume deletion, claim another volume, or establish that an attach or
detach succeeded. Destructive ownership checks and ambiguous operation recovery
still require fresh provider evidence. Invalidate relevant snapshots on mutation.
On 429, preserve the last observation as stale; never treat an incomplete inventory
as an empty fleet or a missing volume.

## 5. Centralize and budget Railway management requests

Move management operations from CLI subprocesses to a shared Go HTTP GraphQL
client. Migrate service, variable, volume, deployment, region and resource-limit
operations so the budget governs actual HTTP requests, not just CLI invocations
that may each generate several requests.

- Batch variable writes with `variableCollectionUpsert`, preserving omitted
  variables and skipping implicit deploys. Send credentials only in HTTPS bodies.
- Reuse HTTP connections and preserve account-token/project-token header semantics.
- Coordinate request pacing and cooldowns by credential/quota scope across
  aliases and controller processes; apply both burst and hourly budgets, with
  conservative configurable defaults and response-header updates.
- Prioritize user-requested lifecycle work above background refreshes, reserving
  capacity for recovery without starving reconciliation indefinitely.
- Honor `Retry-After` and rate-limit reset headers. Persist deferred lifecycle
  work so restarts do not cause a retry burst; apply bounded jitter to retries.
- Retry safe reads after cooldown. For ambiguous mutations, reconcile operation
  state before retrying; never blindly repeat deployment or deletion requests.
- Redact tokens, request bodies and provider error payloads that may contain
  secrets. Do not introduce a preliminary telemetry or benchmarking project.

## 6. Use deployment webhooks as refresh hints

Add a project-scoped webhook receiver for deployment state changes. Railway's
documented webhooks are unsigned: validate a high-entropy endpoint secret, redact
it from access logs, bound payload sizes, deduplicate events and rate-limit the
receiver. Validate project, environment and resource scope before scheduling work.

Webhooks only enqueue/coalesce targeted checks; they never authorize destructive
operations or establish readiness by themselves. Handle duplicates, out-of-order
events and dropped delivery. Agent registration provides runtime readiness;
budgeted API reads provide infrastructure confirmation. Retain periodic slow
reconciliation and bounded deployment polling as recovery paths.

## 7. Roll out without disrupting existing boxes

1. Land protocol, schema, transport and capability-negotiation support additively.
   Existing unregistered workers continue using the current transport.
2. Build the agent into the next worker image and test with isolated disposable
   Railway resources. Do not change existing volume assignments to test it.
3. Deploy controller support through the existing release process.
4. Enroll existing workers by securely installing/starting the agent alongside
   existing processes, one at a time, without redeploying worker compute. This
   migration can use the current Railway SSH path once per worker. If a worker
   cannot safely run the agent alongside its processes, leave it legacy until an
   authorized normal replacement instead of restarting it implicitly.
5. Atomically select agent transport per worker after handshake, assignment and
   session checks pass. Keep rollback explicit; a connection failure must never
   trigger a silent Railway fallback or duplicated command.
6. Enable the shared inventory, management request budget and webhooks. Remove
   legacy hot-path Railway calls once every intended worker is migrated.

Existing streams may disconnect on controller rollout, but worker programs must
survive. Subsequent CLI/browser reconnections use the same runtime session. Keep
schema rollback additive and preserve task results and workspace files.

## Acceptance tests

These are correctness checks, not a measurement phase.

- Make every Railway management operation and Railway CLI invocation fail in the
  test harness: an already-enrolled running box still supports shell attachment,
  task execution/status, Grid, desktop, file transfer and reconnection.
- Simulate persistent 429 responses: connected box sessions remain usable;
  infrastructure work waits and obeys the shared cooldown without a retry storm.
- Restart controller, disconnect agent transport and restart agent independently:
  surviving tmux/desktop sessions remain discoverable; terminal input and tasks
  are not replayed. A worker-compute restart is reported as process loss.
- Reject cross-account access, reused enrollment credentials, stale assignments,
  stale incarnations and duplicate connection owners.
- Verify stream cancellation, slow-client backpressure, real exit codes and
  concurrent viewers; browser/CLI disconnect closes viewers only.
- Verify one shared inventory refresh for concurrent callers, and no provider
  reads caused by UI status polling. Rate-limited reads cannot release capacity
  or trigger deletion based on absence.
- Exercise duplicate/missing webhooks and ambiguous deployment/volume mutations.
- Pass existing Railway lifecycle tests, plus isolated database, real tmux/PTY,
  browser and disposable Railway agent-transport integration tests. Explicitly
  identify skipped integration coverage rather than claiming a full live pass.

## Remaining dependency on Railway

The controller, database and boxes still run on Railway infrastructure. Creating
or replacing worker compute, changing resources, and attaching/detaching/deleting
volumes continue to depend on its API. The promised independence is that ordinary
use of a running, enrolled box no longer depends on Railway's management API or
SSH gateway. A Railway network or compute outage can still interrupt connections.

References: [API limits and retries](https://docs.railway.com/integrations/api),
[bulk variable updates](https://docs.railway.com/integrations/api/manage-variables),
[webhook delivery and authentication](https://docs.railway.com/observability/webhooks).
