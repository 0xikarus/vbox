# Goal: persistent agents with their own desktop and tools

Status: agreed product direction; proposed implementation, not shipped behavior.

## Outcome

Current delivery scope: an MVP with a per-agent view in the existing controller
UI. No Chief of Staff, group-room UI, or multi-agent coordination is included.
Broader runtime sections below describe future scope, not MVP prerequisites.

Give each agent its own persistent vmbox, with its own Linux desktop, browser,
and files. The agent can see screenshots and interact with applications through
dedicated tools. The user can watch the same desktop through the existing VNC
viewer and take over when needed. Browser sessions persist with the box, and the
agent can use named secrets without receiving their values in its tool context.

The agreed runtime boundary is **one agent per separate box**. We do not need
multiple agent desktops inside one box for this goal.

## User experience

Embed the live desktop in the controller's agent view from the first milestone,
alongside chat, status, and takeover controls. Users should not need a separate
viewer or page to inspect their agent. Reuse the existing authenticated desktop
stream; retain fullscreen and direct terminal views as additional options.

Starting a box with Codex, Claude, OpenCode, or shell means: **start desktop → open a visible
terminal → run the selected mode in its managed tmux session**. This is the default
startup experience for all four modes, not an optional manual terminal launch.
The user can inspect the actual running session through VNC at any time while the
box is running. Controller messages and terminal input reach the same process;
opening the desktop must never launch a duplicate agent.

Closing the terminal window detaches its viewer without stopping the managed
session. Reopening or reconnecting the desktop restores a terminal attached to
that session. Repeated attachment must reuse an existing terminal window when
possible rather than accumulating duplicate windows. The web TMUX tab and native
terminal access remain alternate views of the same session.

For Run once, expose the running task in the desktop terminal too, while preserving
its disposable lifecycle. On completion, retain output and exit status in archived
results; inspection after cleanup uses those results and never reruns the task.
Hibernated boxes have no live process to inspect until normal wake/start handling.

1. The user opens an agent's box and gives the agent a task.
2. The agent captures its desktop, inspects the image, and uses mouse and keyboard
   tools to work in the browser or other graphical applications.
3. The agent captures another screenshot to check the result and continues.
4. The user watches the same screen in the existing Desktop tab or native VNC
   viewer. The box's normal terminal remains available.
5. When manual interaction is needed, such as login or 2FA, the agent yields
   control. The user takes over and explicitly returns control when finished.

Closing a viewer leaves the agent and desktop running. Hibernation retains files
but stops processes; waking a box must not imply that an old agent process resumed.

On wake, restore the desktop and start a fresh shell/Codex/Claude/OpenCode session
for the selected mode, with its configured MCP tools connected. Do not resume the
previous agent conversation or replay its transcript/prompts. Persistent files,
browser profile, identity, and tool configuration remain. Historical chat remains
viewable, but is not automatically injected into the new model session.

Inactivity hibernation is configurable, defaulting to four hours, and an explicit
shutdown command also hibernates the box. Shutdown preserves persistent state;
deletion is separate. Active task execution, user input, and accepted work count
as activity; viewer heartbeats and screenshots alone must not keep a box awake.
Define waiting-for-user handling and surface the pending shutdown in the UI.
The controller owns the stop operation so it can reconcile fleet state correctly.

Agent and box have a one-to-one deletion lifecycle: deleting either deletes both.
Revoke agent access, cancel pending work/deliveries, and remove its workspace through
the existing authorized deletion flow. There is no orphan live agent or box.
Shared room history and previously distributed tools require explicit retention
rules; deleting one agent must not delete other agents' copies or room membership.

## Existing foundation

The current runtime starts a 1280×800 TigerVNC/Openbox desktop on `DISPLAY=:99`.
The web workspace and native viewer already expose this desktop to the user.
Interactive shells inherit the display setting, but that alone does not provide
an agent with screenshot or input tools.

VNC uses a private Unix socket. Existing authorization and assignment checks must
remain intact. The Blender preset already provides an example of registering a
local MCP bridge for agents.

Relevant starting points:

- `internal/boxruntime/desktop.go`: desktop installation, startup, and streaming.
- `internal/boxruntime/blender.go`: existing MCP registration pattern.
- `internal/controller/web_terminal.go`: authenticated viewer transport.
- `internal/controller/web/workspace-desktop.js`: browser desktop viewer.
- `docs/AGENT-GUIDE.md`: architecture and lifecycle requirements.

## Proposed tool surface

All desktop screenshots must be captured inside the box, including agent tool
images and UI avatar/desktop thumbnails. Never capture from the web VNC canvas
or the user's local screen. Use one worker-side capture backend against the box's
running desktop (X11 or its private VNC socket), independent of viewer connections.

Return full-resolution images for agent inspection and resized thumbnails for
the per-agent UI. Serve previews through authenticated controller routes with
account/box authorization. Refresh only while needed by visible UI; do not wake
a sleeping box to capture a thumbnail. A cached preview must show its capture
time and stale/sleeping state. Treat previews as private desktop content and
invalidate them when the associated agent/box is deleted.

Run a local desktop MCP server inside each agent's box and register it with the
supported agent clients: Codex, Claude, and OpenCode. Return screenshots as image
content that the model can inspect, with dimensions for interpreting coordinates.
Tool names below are proposed, not an existing API contract.

All three agent clients are in scope for desktop tools, browser-state loading,
secret references, chat, and controller-synced custom tools. Verify registration,
image results, tool invocation, and reconnect behavior separately for each client;
compatibility with one is not evidence for the others. Preserve existing client
configuration. Shell remains a visible terminal mode, not an MCP agent client.

| Tool | Behavior |
| --- | --- |
| `desktop_screenshot` | Capture the current screen and return an image plus dimensions. |
| `desktop_move` | Move to a screen coordinate along a smooth Bézier path; support hovering. |
| `desktop_click` | Click a screen coordinate; support left, right, and double click. |
| `desktop_type` | Enter literal text into the focused application. |
| `desktop_key` | Send a key or keyboard shortcut. |
| `desktop_scroll` | Scroll by a bounded amount at a screen position. |
| `desktop_drag` | Drag from one coordinate to another. |
| `browser_load_state` | Load a user-provided browser-state reference into the box's browser, subject to origin and box scope. Return status, not cookies or tokens. |
| `typeSecret("secret_key")` | Resolve an authorized secret reference and enter it into a verified destination without returning the value to the agent. |

Tools operate on the same screen the user sees, without requiring an open viewer.
Input must be validated, execution bounded, and errors actionable. Calls must
target the current box assignment and fail when the desktop is unavailable or
the assignment is stale. Existing agent configuration must be preserved when
registering the tools.

## Smooth cursor movement

Use cubic Bézier curves for ordinary cursor movement. The agent supplies a target
coordinate; the desktop driver computes the path from its current cursor position
and sends intermediate pointer events. Clicks use this movement before pressing
and releasing the requested button.

Choose control points for a subtle curve, with bounded variation between moves.
Apply acceleration and deceleration using an eased distance progression along the
curve. Scale duration with travel distance within configured limits; short moves
should remain quick. Always land on the exact requested coordinate, keep the path
inside screen bounds, and avoid artificial missed clicks or excessive wandering.

Allow an explicit path or direct movement for precision-sensitive interactions.
Dragging must preserve the held button and use a tightly controlled path rather
than automatically applying decorative curvature across unrelated UI targets.
Human takeover cancels queued movement and safely releases held buttons and keys.
Reconcile cursor position after human input before starting another movement.

This is a visual interaction feature, not a guarantee that websites will classify
the browser as human-operated. Keep path generation testable with a fixed seed
and clock, and bound event frequency so animation does not flood the transport.

## Persistent browser sessions

Chromium is the selected browser for the initial release. Launch it visibly on
the box's existing desktop, with one persistent profile per box. Implement and
verify browser-state imports and secret entry against Chromium first. Additional
browser support is deferred; comparing browsers is not a release prerequisite.
Measure Chromium's actual memory use with representative tasks for capacity
planning. This selection does not claim it uses less RAM than other browsers.

Keep each box's browser profile on its persistent workspace volume. Unexpired
persistent cookies and durable browser storage must survive browser restarts, viewer disconnections,
hibernation/wake, and replacement of compute attached to that same workspace.
Separate boxes must have separate profiles; do not share live profile directories.

Allow the user to import cookies and supported browser storage through a private
upload flow. The agent receives an opaque state reference, not the cookie/token
payload. Validate the format, target origins, and box ownership before applying
it. Explicitly define support for cookies, localStorage, and IndexedDB; reject
unsupported state with a clear message instead of silently dropping it. Coordinate
imports with the browser so live profile writes do not corrupt its state.

Session cookies, browser sessionStorage, and in-memory application state are not guaranteed to
survive process termination. Persistent state also cannot guarantee a valid login:
sites can expire or revoke sessions and require fresh login or 2FA. Preserve the
existing human handoff for these cases.

Treat imported state and stored profiles as credentials: protect uploaded payloads
and backups, exclude values from logs and model-visible results, and provide user
controls to clear a profile or remove an import. Persistence belongs to the box's
workspace lifecycle; a disposable Run once box must not acquire indefinite profile
retention or silently export its session into another box.

## Secrets by reference

### Secret manager and on-demand password creation

Provide a user-facing secret manager populated by both user-supplied credentials
and passwords generated on agent request. The agent can choose an opaque reference
such as `secretForPageN`; it does not need to supply or receive the password.

Proposed tool flow:

```text
secret_ensure(key="secretForPageN", purpose="new_account_password")
    → verify current browser origin and authorization
    → reuse an authorized existing binding, or generate a password if absent
    → encrypt and durably save it in the secret manager before entry
    → return only the reference and created/existing status
typeSecret("secretForPageN")
    → insert the saved value into the approved field
    → return status only
```

Generate passwords in the trusted runtime/service using a cryptographically secure
random generator and a strong default policy. Accept validated site requirements
such as length and allowed characters; never ask the model to invent the value.
Bind each reference to the account, permitted box/agent, verified origin, and an
optional login identifier. A human-readable key alone is not authorization.

New secrets are available to the same creating agent by default. Other agents do
not inherit access through room membership or tool distribution.

Creation must be atomic and idempotent: repeating the request or reconnecting must
reuse the same saved password, not generate a replacement. A conflicting origin or
binding returns a clear error. Generation never overwrites an existing credential;
password rotation is a separate explicit operation. The same reference can fill
password and confirmation fields. It must not generate a new password merely
because an existing-account login fails or a key was mistyped in `typeSecret`.

Show generated entries in the user's manager with site, label, creator, creation
time, allowed scope, and status. Mark newly generated credentials as pending use
until account creation/password change is confirmed; generation or field insertion
alone is not proof that the site accepted them. Retain pending values for retry
and user inspection instead of discarding a possibly accepted password on timeout.
Password generation does not itself submit a form or authorize account creation.

Also support requesting an existing credential from the user via a private input
card. The response goes directly into the manager, outside chat/model context,
and resumes the waiting task with a reference only. Never substitute a random
password when the task needs an existing account's actual credential. Both paths
use the same encryption, scope checks, sanitized results, and audit metadata.

The initial release uses the agreed practical contract: the agent calls
`typeSecret(key)`, the runtime resolves and inserts the value during tool execution,
and the response contains status only. Stronger isolation from unrestricted
shell/browser access is not an initial-release prerequisite. The additional
isolation discussion below limits future security claims rather than blocking
this tool's implementation.

The user creates, updates, and removes named secrets through a private user-facing
flow, outside agent chat. Store values encrypted at rest in a secret service;
grant access by account, box, and permitted destination. The agent may see an
authorized key such as `work_password`, but never receives a value from the secret
tool. Do not provide an agent-facing read-secret operation.

For example, `typeSecret("work_password")` requests entry of that secret. A trusted
component resolves the reference and inserts the value directly into the approved
browser field. Return only success or a sanitized error. Keep plaintext out of
tool arguments/results, transcripts, logs, command-line arguments, environment
variables, temporary files, and the shared clipboard. Audit the reference,
destination, and outcome without recording the value.

Verify the actual browser origin and destination field at insertion time; the
agent's description or mouse coordinates are not sufficient. Reject unauthorized
origins, arbitrary terminal/text-editor targets, and focus/navigation changes.
Password entry should target a password field. Other secret types need their own
explicit destination policy. Honor takeover/pause for secret entry as for other
input. Secret removal must prevent future resolutions; it does not revoke an
already established website session.

The intended guarantee is that the tool does not expose plaintext to the agent.
A stronger guarantee that the agent cannot recover secrets requires isolation:
an agent with unrestricted shell access to the browser's user/profile, browser
debugging access, or unrestricted desktop control may recover credentials or
session tokens. Password masking and screenshot redaction alone do not establish
that boundary. Before claiming stronger protection, isolate the credential/browser
component and constrain agent access to it, including reveal/copy actions and
browser debugging. The destination website necessarily receives the submitted
secret. This security design remains a prerequisite for such a claim.

## Human control

Provide an explicit pause/takeover and resume mechanism so the user and agent do
not compete for mouse and keyboard control. Pausing must prevent new agent input
and account for an input action already in progress. Screenshots can remain
available while the agent is paused.

The mechanism must cover both web and native VNC use; a browser-only flag is not
sufficient. The exact UI and runtime coordination protocol remain implementation
decisions. This controls the desktop tools; it is not a security boundary against
an agent that also has unrestricted shell access.

## MVP: per-agent view in the existing UI

Extend the existing workspace with embedded desktop, its managed terminal,
direct per-agent messaging, status, and takeover/stop controls. Preserve immediate
steering and `/silent` behavior. Reuse existing UI and messaging paths; separate
messaging deployment is a future extraction, not an MVP prerequisite.

- One agent per box, using the existing desktop and workspace lifecycle.
- The screenshot and input tools above, available to agents inside the box.
- Persistent browser profiles and private import of supported cookies/storage.
- Named secret management and destination-checked secret entry, with a documented
  and tested confidentiality boundary.
- Human observation and takeover through existing desktop access.
- Setup that works for newly created boxes and can be explicitly enabled on
  existing boxes without restarting user workers.
- Verification against a real isolated desktop, including screenshot delivery
  to an agent and successful interaction with a graphical application.

The MVP comprises stages 1–3 plus direct per-agent messaging using the existing
backend. Shared tool distribution, group collaboration, routines, and external
machine/connector management remain documented future scope.
Grok Bot's description is inspiration, with separate boxes per agent. Dedicated
web search/fetch tools are excluded for now: agents use their browser for web work.
Cloud coding integrations, image generation, message-draft integrations, template
export, and direct Discord broadcasting are also outside this goal. Agents may
work with repositories in their own boxes.

## Broader runtime scope

### Chat, identity, and collaboration

Reuse the existing messaging backend and expose it through the agent MCP surface.
Maintain persistent agent identity, configuration, individual chat/history, group
rooms, and asynchronous agent-to-agent messages. All room members see the shared
thread. Support creating/updating agents and rooms; keep deletion user-controlled.
Treat room size limits as a product decision rather than copying an arbitrary
limit from the reference runtime.

No Chief of Staff role is planned. The MVP shows the selected agent and its
conversation/desktop in the existing UI. Rooms and delegation remain future scope.

New actionable messages steer the recipient immediately, including while busy,
using the agent client's supported steering mechanism. Do not silently defer them
until task completion. A leading `/silent` marks a message as stored history only:
no interruption, automatic agent turn, or wake. Preserve the original message and
store delivery mode separately. Show unsupported or uncertain steering explicitly;
never simulate support by blindly pasting into a permission prompt.

Current source contains direct box message routing, chat-group routes, persisted
messages, and delivery reconciliation in `internal/controller/management.go`,
`management_store.go`, and `server.go`. This is an existing foundation, not a reason
to build a second chat store. README currently says the earlier Coworker MCP and
inter-agent adapters were removed. Locate and reconcile that earlier integration
before deciding whether to restore its adapter or add a thin MCP adapter over the
current APIs; an active chat MCP bridge has not been verified by this review.

MCP exposes chat operations; the backend owns durable history, delivery, identity,
and authorization. Bind sender identity to authenticated agent credentials rather
than trusting a supplied source box ID. Preserve idempotent delivery and prevent
unbounded reply loops. Waking a recipient uses existing allocation rules.

### Memory, skills, and routines

Messaging is a separate subservice as described below; memory and scheduling remain
distinct capabilities and should not be stored implicitly as terminal transcripts.

Provide durable per-agent memory, explicitly shared user memory, and reusable
skills, with user inspection/editing and clear access boundaries. Memory survives
agent restarts and box hibernation; live process continuity is not implied.

Support scheduled routines and event listeners through connected services. Store
schedule/timezone, enabled state, trigger identity, execution history, and failure
status. Deduplicate events and define missed-run, retry, and concurrency behavior.
Background actions retain the same permissions as foreground actions.

### Background work and user interaction

Provide delegated tasks, a durable todo queue, progress, steering, and cancellation.
Delegated agents get separate boxes; they do not silently share a parent's desktop.
Distinguish queued, running, waiting for user, failed, cancelled, and completed work.
An agent's claimed completion and a process exit are separate evidence.

Extend chat with attachments, image embeds, questions, approval requests, and
login/2FA handoff. Define which requests suspend a task and how a user response
resumes it without replaying completed actions. Show recent actions and provide
stop/takeover controls.

### Connectors, registered machines, and files

Support discovery, installation, authentication, renaming, removal, restart, and
custom instructions for MCP connectors, including custom URLs and local commands.
Refresh tools after installation/authentication when supported by the client;
otherwise clearly request a session reconnect. Keep account credentials scoped.

Allow explicitly registered machines as additional targets. Every action on a
registered personal machine requires local approval and an explicit machine ID;
never silently fall back to another machine. Disconnecting or revoking a machine
invalidates pending access.

Provide explicit copy-to-box/copy-from-box operations with source and destination
identities, transfer status, and overwrite handling. Attachments retain their
origin until deliberately transferred; transferring files must respect account
and machine permissions.

### Action review

Provide a common review mechanism for shell, desktop, MCP, routines, delegated
launches, and custom tools. Blocked actions surface a concrete approval request.
Approval is bound to the actual action, destination, and relevant version, with
expiry and cancellation; it must not authorize unrelated future actions. Preserve
existing user authorization and avoid repeated prompts for already covered work.

### Agent-authored tools

Allow an agent to write reusable scripts or local MCP tools inside its own box,
test them on disposable fixtures, and register them for later use. Each tool has
a name, description, input/output schema, version, entry point, declared access,
and execution limits. Keep source available for inspection and support disabling
and rolling back versions. Initial scope is private to its authoring agent/box;
sharing with other agents is an explicit operation. Sync registered tool versions
to a controller-managed, account-scoped registry so they survive loss of the
authoring box and can be sent to other agents.

The controller stores immutable versioned bundles containing source, manifest,
dependency lock information, content digest, author/provenance, and validation
status. Sync registered versions, not arbitrary workspace files. Exclude secrets,
browser profiles, credentials, and local environment files from bundles. Package
dependencies reproducibly; identify required system packages and compatibility
constraints instead of copying machine-specific environments.

An agent can list authorized tools and send a specific version to another agent.
The controller records the recipient and delivery status; the recipient retrieves
and verifies the bundle, installs it in its own box, and exposes it through its
tool registry. Queued delivery survives disconnection or hibernation and does not
silently provision compute. Use the normal allocation policy when work requires
waking a recipient. Retries must not create duplicate installations.

Distribution does not transfer the author's permissions or secrets. Validate the
bundle and evaluate activation under the recipient's existing authorization;
request approval only for additional access. Resolve secret references against
the recipient's own authorized bindings. Automatically update distributed tools
after the new immutable version passes the defined validation suite and recipient
compatibility checks. Increased permissions still require authorization. Activate
at a safe boundary: finish in-flight calls on their original version and use the
new version for subsequent calls. Show update/test status and support disable and
rollback, with per-agent install status. Failed or untested versions stay inactive.
Removing a registry version prevents future distribution but does not imply that
existing copies have been deleted; disabling installed copies is a separate action.

Separate authoring/testing from activation. Tools using already authorized local
capabilities can be activated within that scope; new access requires the common
review mechanism. A custom tool cannot grant itself credentials, machine access,
or exemptions from review. Secret references remain references and must pass
through the same destination-checked secret service.

Run generated code under enforced runtime permissions with bounded time/output
and cancellation. A manifest's declared permissions are not enforcement. Changes
to code or dependencies invalidate approvals tied to an earlier version. Do not
let custom tools mutate the trusted tool broker or approval service. As with
secrets, unrestricted shell access must be reconciled with any claimed isolation.

## Messaging architecture and broader verification

### Messaging subservice design

Extract current messaging operations behind an interface first, then provide
`cmd/vmbox-messaging` backed by `internal/messaging`. Keep existing controller
routes as authenticated proxies so migration does not break clients. The service
owns threads, membership, messages, attachments metadata, interaction requests,
and delivery state. The controller retains agent identity, box allocation,
wake/hibernate policy, and worker transport. It also retains the shared tool
registry; messaging carries tool-version references, not executable bundles.

Start with PostgreSQL, a messaging-owned schema and role, and private authenticated
HTTP between services. No additional queue broker is required initially. Save
messages and recipient delivery records in one transaction. The controller claims
delivery records using expiring leases and fencing tokens, resolves the current
assignment, and delivers via the existing worker runtime. Carry stable delivery
IDs through to worker deduplication. Ambiguous terminal submissions require
reconciliation, not blind replay. Message delivery is not task completion.

Keep a durable, ordered event stream for reconnecting UI clients. Derive sender
identity from scoped credentials. Enforce account and thread membership on reads,
sends, attachments, and subscriptions. Distinguish room visibility from recipients
asked to act; do not automatically trigger every agent on every reply.

The initial reply contract uses an explicit `chat_send` MCP call with thread and
reply correlation. Show terminal output separately; do not claim it is structured
chat history. Prove send → worker delivery → agent reply → UI rendering for each
agent client. Implement immediate steering and `/silent` history-only delivery;
define waiting-for-user, interruption, and exit
without a reply before treating the adapter as a durable conversation runtime.

Suggested tables: participants (references to controller identities), threads,
thread_members, messages, deliveries, attachments, message_attachments,
interaction_requests, and events. Use account-scoped foreign keys and stable IDs;
validate replies belong to the same thread. Specify direct-thread uniqueness,
membership history visibility, retention, and deletion before final SQL migrations.
Participant identities and retained history must survive compute replacement;
define how they are retained when a logical box or task is deleted, because current
message relationships depend on those entities. Event cursors cover delivery and
interaction changes as well as messages; a thread message sequence alone is not
a complete event cursor.
Use request hashes to reject idempotency-key reuse with changed content. Ensure
sequence allocation and retry handling remain correct under concurrent sends.
Store attachment bytes privately outside message JSON; never store secret values
in chat. Preserve existing message IDs/history through a staged migration with one
writer and a rollback strategy; the conversational SQL sketch is not a migration.

### Verification

- Reuse existing chat records and verify authenticated sender identity, room
  membership, cross-account denial, deduplication, and restart-safe delivery.
- Verify memory scopes and routine execution across restart, duplicate triggers,
  missed schedules, cancellation, and waiting-for-user states.
- Verify connector activation/removal and revoked machine access; local actions
  cannot execute without their required local approval.
- Verify file origin/destination and overwrite handling with disposable files.
- An agent authors, tests, activates, and calls a versioned example tool. Verify
  timeout/cancellation, rollback, and denial of undeclared or unapproved access.
- Sync that tool to the controller and deliver the pinned version to a second
  disposable agent box. Verify bundle integrity, account boundaries, offline
  delivery/retries, recipient permissions, absence of credentials in the bundle,
  and successful use without access to the author's workspace. A new synced
  version must update automatically only after validation/compatibility checks;
  failed tests block activation and in-flight calls retain their original version.
- Approval tests cover changed arguments/code, expiry, cancellation, and rejection
  without executing the blocked action.

## Acceptance criteria

1. An agent receives a real screenshot as an image and can describe visible
   content in its own box.
   Verify agent screenshots and UI previews both originate from worker-side
   capture with no VNC viewer connected. Thumbnail reads enforce account scope
   and never wake sleeping boxes; cached images are clearly marked stale.
2. It can click, type, use shortcuts, scroll, and drag, then verify the resulting
   screen state with another screenshot.
3. The user sees those actions on the same desktop through VNC.
4. Human takeover blocks agent tool input until explicitly resumed.
5. Two disposable test boxes demonstrate that tools and input stay within their
   respective boxes.
6. Closing and reopening the viewer preserves the running desktop; hibernation
   and assignment changes produce clear tool failures or reconnection behavior.
7. Tool registration preserves existing client configuration, and no public VNC
   listener or controller credentials are introduced into the tool interface.
8. A disposable browser profile retains test cookies and supported durable storage
   across browser restart and workspace reattachment; imports enforce box/origin
   scope and report unsupported formats. Expired sessions allow human login.
9. Using a synthetic secret, verify successful entry into an approved test login
   field and absence of its value from model-visible results, transcripts, logs,
   clipboard, process arguments, and environment. Test wrong-origin, changed-focus,
   unauthorized-box, paused-control, and removed-secret failures.
   Verify on-demand password creation with disposable signup fixtures: durable
   save before entry, concurrent/retried requests returning the same reference
   and value, confirmation-field reuse, origin conflicts, pending-use status,
   and no plaintext in model-visible output. Test private user-supplied secret
   requests and cancellation without including submitted values in chat history.
10. Document and test the isolation boundary against shell/profile access, browser
    debugging, and reveal/copy attempts before claiming secrets cannot be recovered
    by the agent. Never use real credentials for these tests.
11. Verify Bézier movement starts at the current cursor position, stays in bounds,
    ends at the exact target, and completes within configured timing limits. Test
    clicks after arrival, controlled dragging, and cancellation during takeover.
12. Starting with Codex, Claude, OpenCode, or shell automatically opens the desktop and a
    visible terminal for the selected managed session. Verify that controller
    messages and all terminal viewers reach the same process, closing/reopening
    the window preserves it, and reconnects do not create duplicate agents.
    Completed Run once tasks remain inspectable through archived output without
    changing cleanup behavior or replaying execution.

Tests that create resources must use explicitly identified disposable resources.
Record which behavior was tested locally and which was verified on a real worker.
Implementation does not authorize deployment, restarting user workers, or deleting
their data.

## Implementation decisions still to resolve

- Screenshot and input backend: use the existing private VNC connection or a
  local X11 implementation, based on correctness and dependency cost.
- How desktop startup and tool registration fit interactive and one-shot agent
  launch paths without changing their lifecycle contracts.
- How takeover is exposed to native viewers and enforced across tool sessions.
- Screenshot size limits and action timeouts, validated with the supported clients.
- Browser profile placement and supported import formats for each storage type.
- Secret-service placement, browser origin verification, and isolation needed to
  prevent an agent from extracting credentials outside the secret tool.

## Review and proposed implementation sequence

The scope above is the intended outcome. The sequence below is a proposed delivery
order; listing a capability does not mean it is implemented or deployed.

1. **Inspectable startup.** Start the desktop and visible managed terminal for
   Codex, Claude, OpenCode, and shell. Verify shared process identity, reconnects,
   task output, and cleanup. Audit interactive and Run once support separately:
   OpenCode's interactive launch path does not establish one-shot support.
2. **Desktop tools and takeover.** Implement screenshots and input, Bézier movement,
   cancellation, and shared control ownership. Verify actual image/tool exchanges
   with all three agent clients and a real desktop. Select vision-capable models
   for screenshot-driven tasks; a compatible client alone does not ensure its
   selected model can inspect images.
3. **Browser state and secrets.** Use Chromium as the initial browser. Measure
   idle, active, and peak memory plus login persistence and browser-tool
   compatibility with representative tasks. Build the
   trusted browser bridge and settle its isolation boundary before offering a
   stronger secret-confidentiality guarantee. Validate imports and user handoff.
4. **Chat and shared tools.** Reconcile the existing messaging/MCP work, then add
   authenticated agent messaging and controller-backed tool versions, distribution,
   installation, and rollback. Establish tool execution permissions before
   activating agent-authored code through the managed registry.
   Prove a minimal reply round trip with one real agent before the service split,
   then verify all three clients. Extract messaging behind an interface before
   switching to the separate service; preserve history and current API routes.
5. **Durable coordination.** Add memory, skills, routines, delegated task queues,
   rich chat requests, and progress/steer/stop controls on the existing lifecycle.
6. **External access.** Add connector management, registered-machine approval,
   and explicit file transfers. Review enforcement is a dependency of each stage
   that introduces privileged actions, not a feature postponed until this stage.

### Decisions that need explicit implementation designs

- **Control ownership:** serialize agent actions and coordinate human input across
  native and web viewers at a shared runtime boundary. Initial takeover coordinates
  managed tool input; preventing deliberate bypass via unrestricted shell/VNC/X11
  access is outside its initial guarantee.
- **Secret isolation:** the visible agent terminal and browser share a desktop.
  A separate secret vault alone does not prevent extraction through that desktop
  or the agent's shell. The initial guarantee covers tool execution and results;
  stronger isolation is deferred and is not a release blocker.
- **Short sessions and cost:** persistent identity/files do not require always-on
  compute. Implement configurable inactivity hibernation (four hours by default)
  and explicit shutdown, with protections for running tasks and defined handling
  of user handoffs. Ten minutes of use is not a ten-minute compute bill
  unless the worker stops consuming resources afterward. Retained volumes,
  controller/database services, and any idle fleet capacity still incur costs.
  Inspect the provider stop behavior and meter real usage before advertising a
  per-session price. These are planned defaults, not changes to running workers.
- **Browser choice:** Chromium is selected for the initial release. Additional
  browsers are deferred; actual RAM consumption still needs worker measurements.
- **Release evidence:** maintain a per-client capability matrix distinguishing
  implemented, locally tested, worker-tested, and deployed behavior. Reconcile the
  substantial existing working-tree changes before implementation and release.

Current implementation and verification: [desktop MVP guide](AGENT-DESKTOP-IMPLEMENTATION.md).
