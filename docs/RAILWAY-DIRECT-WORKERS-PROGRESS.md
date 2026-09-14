# Direct-worker implementation status

The full scope remains [RAILWAY-DIRECT-WORKERS.md](RAILWAY-DIRECT-WORKERS.md).
Existing user boxes must not be shut down or restarted during implementation.

## Implemented locally

- `internal/workerprotocol`: multiplexed WebSocket streams, independent EOF,
  bounded receive windows, separate control queue, heartbeat timeout, monotonic
  stream IDs and cancellation. No automatic replay or transport fallback.
- Durable operation claims and exit-code journal, recording request hashes rather
  than secret-bearing arguments/output. Incomplete claims remain ambiguous.
- Exact-argv command execution with a mandatory binding validator, separate
  stdout/stderr and explicit exit records. Output records have bounded size.
- Local WebSocket/process tests cover flow control, slow-consumer isolation,
  disconnect semantics, assignment rejection, exact arguments/input/output and
  durable exit status. Initial tests pass with the race detector.

- `cmd/vmbox-worker-agent` and `internal/workeragent`: private configuration,
  durable credential preparation, HTTPS enrollment, authenticated versioned
  WebSocket handshake, reconnect backoff, per-command local binding validation.
- Controller endpoints for owner-issued slot enrollment, one-time credential
  exchange and worker connections; additive `direct_workers` schema tracks
  credential hashes, revocation, connection epochs and expiry. A global lease
  rejects a second active controller until connection-owner routing exists.
- Real disposable PostgreSQL plus local TLS integration verifies enrollment,
  duplicate/revoked credentials, cross-account boundaries, stale epochs and
  direct command output/exit status with no Railway provider configured.
  Agent/protocol/controller targeted tests pass with the race detector; vet passes.

- Opt-in Railway provider wrapper resolves enrolled connections through the
  database and live agent peer, executes and streams over that peer, and rejects
  stale handoffs or offline agents without SSH fallback. Running streams recheck
  their assignment and connection every second. Legacy workers keep their
  existing transport while the feature and per-worker selection remain disabled.
- Native/browser connection fences accept agent incarnations; connection display
  metadata uses the stored slot instead of a Railway inspection for agent mode.
- Enrolled runtime bootstrap uses the same fenced agent stream, including asset
  stdin, and cannot fall back to Railway when disconnected.
- Disposable PostgreSQL/TLS integration now also forbids backing Railway
  execution, connection lookup, inspection and bootstrap; verifies exact exit
  status, binary transfer beyond the flow-control window without captured output,
  and cancellation of an active command after a database assignment change.
  Bootstrap routing uses a fingerprint-response fixture; it does not prove live
  package installation. These targeted tests pass under the race detector.

- Added owner-authenticated `GET /v1/logical-boxes/{id}/worker/stream` for CLI
  operations. It validates the running assignment and live agent, rejects browser
  origins and worker credentials, caps active streams, rechecks owner token and
  assignment during execution, and propagates client closure without replay.
- `internal/transport.Worker` connects to the configured HTTPS controller only,
  refuses redirects, carries exact argv and binary stdin/output with bounded
  capture, and requires explicit exit records. It never uses a provider-supplied
  address to send the controller credential.
- CLI native attach, local desktop forwarding and authentication upload now
  dispatch by resolved transport. Direct terminal attachment uses runtime PTY
  framing and resize messages; legacy SSH continues unchanged. A cancellable
  terminal reader finishes before another attachment can read the same terminal.
- Integration now covers actual client → TLS controller → TLS agent command and
  binary streams, stale incarnation rejection, worker-token rejection and active
  assignment cancellation while every backing Railway call is forbidden. CLI and
  transport regression suites pass under the race detector. Additional local TLS
  tests cover terminal framing and replacement controller tokens, and a real PTY
  test proves initial resize and input-reader cancellation preserve the next
  attachment's input. Redirect rejection tests and targeted vet also pass.

- Worker images now build and include `vmbox-worker-agent`, with its checksum in
  the image manifest. Normal entrypoint startup launches its supervisor only when
  private enrollment configuration exists; the trust-configuration helper exits
  before this startup path. Existing workers have not been redeployed.
- Agent and supervisor have separate private, non-symlink, exclusive process
  locks. The supervisor restarts only its agent child with bounded backoff and a
  minimal environment, sends SIGTERM on shutdown and waits for that child. It
  never manages the worker's tmux/desktop or other workload processes.
- Supervisor tests verify duplicate/symlink/public-lock rejection, recovery from
  an agent exit, removal of inherited Railway credentials, child cleanup and
  survival of an independent disposable local process. Race tests and vet pass;
  the agent cross-builds successfully for Linux amd64 and arm64. Entrypoint shell
  syntax passes. A full image build and live sidecar installation remain pending.

- Owner enrollment now installs a sidecar through one explicit legacy bootstrap
  stream; it no longer returns enrollment credentials. Payloads contain only
  fixed-name private config/binding assets and the bounded agent binary. The
  installer checks architecture, refuses an existing config, and starts a detached
  supervisor without restarting compute or runtime sessions. Credentials remain
  outside the workspace and out of argv and response bodies.
- Installation holds a finite assignment lock against concurrent release, keeps
  transport disabled pending verification, refuses duplicate enrollment, and
  cleans up an unused enrollment only if no remote installation was attempted.
  An ambiguous remote failure preserves identity for recovery instead of replay.
- Payload tests verify shell syntax, private archive modes, exact binary/binding
  preservation and cross-slot rejection. Disposable database/TLS tests exercise
  the owner route with a capture-only bootstrap fixture, verify credential-free
  responses and pending transport state, and reject duplicate installation.
  These are local installer/route checks, not a live sidecar installation pass.

- Installation now records a complete fenced session baseline before changing
  the worker. Owner-only `POST /v1/worker-slots/{slot}/activate` observes sessions
  through the authenticated agent, requires all baseline session IDs/names/tmux
  incarnations to survive, then switches transport under assignment row locks
  and an exact connection-epoch/incarnation check. It does not rebind tmux or
  create/restart sessions. Missing, partial, stale or recreated sessions leave
  the existing transport selected.
- Disposable database/TLS activation tests prove missing sessions cannot enable
  transport, matching sessions can, and stale epochs cannot activate. A separate
  real tmux test uses a private disposable socket to verify unchanged sessions,
  additional sessions, recreated sessions and stale/partial inventory handling.
  These tests pass with the race detector; controller vet and diff checks pass.

- Existing GraphQL management operations now use a Go HTTP client rather than
  `railway api` subprocesses. Token type selects the documented authorization
  header; variables remain in HTTPS bodies, redirects are refused, response sizes
  are bounded, GraphQL errors are checked, and provider error bodies are omitted
  from returned errors. Mutations are never automatically replayed.
- A credential-hash-scoped local budget coordinates API calls across provider
  aliases/instances, applies request spacing and a rolling hourly bound, lowers
  limits from response headers, and shares 429/exhaustion cooldowns. Defaults are
  one request per second and 80 per hour. A `RequestBudget` interface supports the
  still-required persistent controller-wide implementation and configured quotas.
  This budget currently governs HTTP GraphQL calls, not remaining CLI commands.
- Railway provider scenario tests now adapt their GraphQL fixtures at the HTTP
  boundary. Dedicated local TLS tests verify account/project token headers,
  request-body variables, sanitized GraphQL errors, shared cooldowns, independent
  token scopes, hourly limits, HTTP-date Retry-After, redirect rejection and no
  ambiguous mutation replay. The provider suite passes under the race detector.
  Semantics checked against [Railway API documentation](https://docs.railway.com/integrations/api).

- Controller-created Railway providers now use `PostgresRequestBudget` instead
  of the local gate. Credential-digest rows serialize permits, persist cooldowns
  and rolling-hour usage, and use database time after acquiring the row lock.
  Waiting holds no transaction and reserves no future permit; database failures
  deny API calls. Configurable hourly/spacing limits are documented in
  `CONTROLLER.md`. Separate controller connections share the same quota scope.
- Disposable PostgreSQL tests verify concurrent gate instances cannot exceed a
  shared hourly limit, recreating a gate preserves usage, independent credential
  scopes remain usable, API 429 cooldowns survive across clients, late successful
  responses cannot clear cooldowns, and expired usage releases permits.
- Worker creation now writes nonempty metadata/environment values in one
  `variableCollectionUpsert` HTTP request, preserving omitted variables and
  suppressing implicit deployments. Variable reads also use HTTP and stable
  service IDs. Ambiguous batch results are reconciled by reading values rather
  than repeating the mutation. Local TLS tests verify private batch bodies,
  preservation flags, sealed-variable handling and zero CLI variable calls.
  Schema verified against [Railway's variable API](https://docs.railway.com/integrations/api/manage-variables).

- Deployment listing and source-based submission now use HTTP GraphQL with
  stable service/environment IDs. A returned deployment ID is polled through the
  single-deployment query, avoiding full-list polling. An ambiguous submission
  uses a read-only before/after comparison and rejects multiple new candidates;
  it never repeats the deployment mutation. Malformed/null deployment inventories
  are errors, not empty lists. Readiness/visibility contexts also bound quota waits.
- Local HTTP tests verify direct returned IDs, interrupted submissions with one
  or multiple new records, exact-ID polling, rejection of unavailable inventory,
  one mutation and zero CLI deployment calls. Existing provider lifecycle and
  polling tests pass under the race detector. No real deployments were submitted.
  Schemas checked against Railway's official
  [service](https://docs.railway.com/integrations/api/manage-services) and
  [deployment](https://docs.railway.com/integrations/api/manage-deployments) references.

- Service validation/discovery now uses a paginated environment-scoped HTTP
  inventory, including image source and serving deployment replica state. It
  validates environment/project IDs, required fields, cursor progression and
  unique service IDs. Only a complete successful read replaces cached aliases;
  a failed read preserves the last valid inventory. A serving deployment takes
  precedence over a newly building deployment, and stopped/crashed/removed
  replicas do not appear healthy because of a historical SUCCESS status.
- Box listing now fails when any candidate's metadata read fails, including 429,
  rather than returning a successful list with missing boxes. Local HTTP tests
  cover pagination, incomplete/wrong-scope data, stale-cache preservation, alias
  removal after a complete empty read, serving replica states and metadata rate
  limits. Provider race tests and vet pass. This is not yet the shared five-minute
  snapshot scheduler; that coalescing/background-refresh work remains required.
  Inventory fields and mapping verified against the public
  [Railway CLI schema](https://github.com/railwayapp/cli/blob/master/src/gql/schema.json)
  and [service summary implementation](https://github.com/railwayapp/cli/blob/master/src/commands/output/service_summary.rs).

- Volume creation/attachment/detachment/deletion now use the budgeted HTTP API.
  Attachment updates always include the configured environment ID; detachment
  explicitly sends `serviceId: null`. Mutation results must confirm application
  or return an ambiguous outcome, with no automatic replay. Existing provider
  ownership/attachment prechecks and post-mutation reconciliation remain in place.
  Whole volume-operation contexts now bound API quota waits as well as polling.
- Local HTTP tests check exact project/environment/service/volume scoping, private
  structured inputs, null detachment, unconfirmed mutation rejection, one request
  and zero CLI mutation calls. Existing provider ownership, creation ordering and
  deletion tests pass under the race detector; vet/diff checks pass. Volume
  inventory still uses the CLI and must be migrated, including cross-environment
  deletion evidence, before live rollout. No real volumes were changed.
  Mutation fields checked against the official volume API and public CLI schema.

- Volume inventory now uses paginated project-level HTTP queries and checks
  every returned volume's project and environment instances. Attachment decisions
  compare immutable service IDs; service names are display metadata. Missing
  attachment fields, invalid pagination and truncated nested instance inventories
  are errors. Unknown state is not converted to READY. The volume CLI fallback
  has been removed.
- Global volume deletion is rejected when another environment has an instance
  or when the configured environment has no verified instance. Attach/detach also
  require that environment instance; missing/deleting target volumes cannot cause
  an attachment mutation. These checks augment the existing exact ID/name and
  ownership guards.
- Local HTTP tests verify complete volume pagination, cross-environment deletion
  rejection, missing service-ID/page evidence, and wrong-project/truncated data.
  Provider race tests and vet pass. No existing volumes or boxes were changed.
  Shared snapshot refresh, lifecycle agent provisioning and live rollout remain
  unfinished.

- Stop and service deletion now use the shared budgeted HTTP client. Stop selects
  the newest successful deployment in the scoped deployment inventory, matching
  the public Railway CLI's `down` behavior, and removes its exact ID. Service
  deletion retains fresh inspection, ownership and volume guards. Both operation
  contexts include quota waits in their deadline. Missing/false mutation
  confirmations are ambiguous errors and never cause automatic replay.
- Local tests verify exact successful-deployment selection despite a newer build,
  no mutation without a successful deployment, exact service deletion IDs,
  cross-account rejection and missing/false confirmations without replay. Provider
  race tests, the repository Go suite and provider vet passed. The repository run
  omitted the disposable database URL, so database tests were skipped in that run.
  No live resources were touched. Logs and billing usage still invoke the CLI;
  the remaining transport/lifecycle/snapshot/webhook rollout work is not complete.

## Still required

### Nonblocking inventory observations

`GET /v1/inventory` now uses the shared Railway observation without starting or
waiting for an HTTP read. The background scheduler refreshes registered scopes.
The response distinguishes an unavailable observation from an observed empty
inventory, carries the last observation timestamp, and reports stale/in-flight/
failed refresh state. Failed refreshes retain prior boxes. Logical boxes still
come from the controller database. `vmbox ls` displays observation state and avoids
claiming an unobserved inventory is empty. Lifecycle `List`/`Inspect` behavior is
unchanged by this presentation path.

Provider race tests prove repeated fresh/missing/stale observation reads perform
no provider requests. Controller tests exercise both legacy and observation
providers, with `List` forbidden for the observation path, retaining fleet-slot
filtering and response sanitization. CLI tests cover missing observations and
failed-refresh visibility. Provider/controller/CLI race tests passed, and the
provider/controller database regression run passed with the disposable database
enabled. Agent runtime observations and browser status presentation remain open.

### Recovery of configured, pending installations

An owner-only `POST /v1/worker-slots/{slot}/recover` endpoint now handles an
installation that already has its private configuration but has not activated.
It requires the original session baseline and a matching running assignment,
renews only the existing enrollment token's expiry, and preserves its hash,
credential, binding and baseline. The installed agent verifies public expected
identity from stdin against private files. The recovery shell leaves a held
supervisor lock alone and starts a missing supervisor without replacing files or
stopping any process. Activation still performs the original session checks.

The full controller database race suite passed, including recovery token/baseline
preservation and rejection of a missing baseline. Agent identity/private-file
tests passed. The shell's held-lock and missing-supervisor paths passed in an
isolated, network-disabled Debian container with a disposable stand-in agent.
An Alpine run failed because its `setsid` differs from production's util-linux;
the Debian run uses the worker's base image. No existing worker was recovered or
modified. Recovery before a configuration file was installed still requires a
separate guarded bootstrap retry and is not implemented by this endpoint.

### Free-slot maintenance bindings

Direct resolution now supports an explicitly free slot with no logical-box
reference and a cleared assignment fence. Its maintenance binding uses a
controller-generated `compute-slot:` identity and a hash of the account, slot
and assignment generation. Invalid/mismatched assignments, occupied slots with
missing box records, and leftover fences cannot become maintenance authority.
This identity changes on reservation and cannot authorize owner logical-box
stream endpoints. Storage attachment/deletion evidence remains provider-owned.

The disposable database/real TLS-agent integration verifies assigned → free →
assigned transitions, successful maintenance execution, rejection of the old box
connection, rejection of an uncleared fence, and rejection of the old maintenance
connection after reassignment. It passed under the race detector with all Railway
calls forbidden. The full controller race suite also passed with the disposable
database enabled. This changes neither deployment nor volume lifecycle behavior;
normal hibernation still follows the existing explicit lifecycle. New/replacement
worker enrollment, persistence across compute replacement, installation recovery
and live rollout remain unfinished. No user worker was changed or shut down.

### Controller synchronization of assigned workers

Agents now include their private local binding in the authenticated handshake.
The controller verifies its account/slot/incarnation scope when `binding-v1` is
advertised. Resolving an enabled assigned worker serializes binding transitions
on that peer, checks database assignment and connection ownership, sends the
compare-and-swap request if needed, awaits acknowledgement, and checks database
authority again before returning the connection. A missing acknowledgement closes
the control peer so reconnect can observe the actual applied state; it does not
guess the previous binding or restart worker compute.

The disposable PostgreSQL plus real TLS-agent integration now advances a test
assignment, resolves its direct connection, verifies the private binding update,
and proves the old binding is rejected. The backing provider panics on Railway
API/SSH execution, inspection or connection lookup, so those paths cannot supply
the successful result. This integration passed under the race detector. Tests
used only the identified disposable database's private schema and temporary
local agent files. Free-slot transitions, new-worker provisioning, partial-install
recovery and live rollout still remain; no user worker was modified.

### Assignment control protocol

The multiplexed protocol now supports a separate `rebind` request, mutually
exclusive with executable arguments. The agent advertises `binding-v1` and routes
these requests to an atomic, fsynced compare-and-swap of its private binding file.
Both expected and desired bindings must match the configured account, slot and
current process incarnation and contain a box/fence. Concurrent transitions are
serialized; a stale expected binding is rejected. An already-applied desired
binding succeeds idempotently after an acknowledgement loss. No workspace
command, volume operation or runtime restart occurs in this handler.

Owner browser/CLI execution endpoints reject this control type explicitly.
Execution-only protocol consumers also reject it instead of treating an empty
argument list as a command. Local race tests cover protocol separation, scope,
cancellation, concurrent compare-and-swap, private file mode and idempotent
acknowledgement recovery; worker agent/protocol/controller suites and vet pass.
Database integrations were not enabled in this run. Controller lifecycle wiring,
free-slot binding semantics and authoritative assignment synchronization are
still required before this capability is used in production.

### Agent-side active assignment revocation

The worker executor now revalidates the agent's authoritative local binding every
250 ms during an active operation. A changed/unavailable binding cancels only
that operation's process group and closes its stream, including when output is
blocked waiting for client credit. An interrupted operation retains its durable
claim without a fabricated exit result and cannot be automatically replayed.
Normal command completion/cancellation is distinguished from binding revocation.

Local race tests cover a sleeping command, an output-blocked command, survival of
an unrelated disposable process, and retained incomplete journal claims. Worker
protocol, agent and controller race suites passed; database integrations were
not enabled in this run. No existing worker processes were touched. This closes
the active-stream validation gap but does not itself implement controller-driven
assignment updates or new-worker enrollment.

### Shared inventory implementation update

The controller now owns one Railway inventory cache shared across provider
instances. Keys hash the effective token, authentication mode, project and
environment; tokens are not stored in map keys. Complete `List` results include
the ownership/metadata reads, so concurrent provider aliases reuse those reads
too. Results are copied before returning to callers. The shared read has its own
bounded controller context, so cancelling one viewer does not cancel other
viewers' work.

Recently used scopes refresh in the background every approximately five minutes
(4.5–5.5 minutes with jitter); the scheduler checks every 15 seconds. Scopes idle
for 15 minutes are evicted after pending work finishes. Every HTTP mutation
invalidates the scope before and after the request, including ambiguous failures.
Generation checks prevent pre-mutation reads from overwriting newer observations.
`Inspect`, volume evidence and deployment selection remain fresh reads.

Failed refreshes preserve the prior snapshot internally and return an error;
they cannot report an empty fleet. UI presentation of stale observation age and
agent-derived status remains unfinished. These cache changes are local and have
not been deployed. Provider/controller race tests and vet passed, including
coalescing, independent cancellation, scope separation, mutation invalidation,
fresh inspection, background refresh, idle eviction and failed-refresh retention.

This package is not yet connected to production. Complete end-to-end assignment lifecycle binding and active-stream revocation,
observations, full CLI/browser/lifecycle acceptance coverage,
shared inventory and management HTTP client/budget,
webhooks, disposable integration tests and gradual live enrollment from the plan.
Recovery of partially installed enrollments and new-worker enrollment provisioning are
still required; the image does not enroll itself automatically.
The agent currently validates a private local binding file; provisioning and
updating that file from the authoritative controller assignment still needs wiring.
Running-box routes must use database validation and the live worker peer before
this can replace Railway on any production connection.

The local PostgreSQL test instance is explicitly disposable:
`vmbox-direct-worker-test-20260911`, no existing/user volumes. The TLS integration
executes a local `printf` command and exit code 7; it is not Railway live proof.

No worker installations, deployments, process shutdowns or restarts have been
performed for this implementation. Unrelated working-tree drafts are preserved.
