# vbox: guide for future agents

Architecture, lifecycle rules, and implementation entry points. Check the source
for current behavior.
Start with the [setup guide](../README.md). The [desktop MVP guide](AGENT-DESKTOP-IMPLEMENTATION.md) records the newer desktop/secret/idle implementation and verification. This document explains where to work
and the distinctions that must survive future changes.

## What the product is

The controller owns provider configuration, credentials, logical boxes, persistent
storage, and a reusable fleet of compute slots. The CLI is controller-first and
provider-agnostic. A logical box is **not** a fleet service: its workspace volume
can outlive, and later attach to, a different compute slot.

- `vbox BOX`: persistent interactive shell/tmux workspace; launch agents yourself.
- Web box workspace: start the selected managed agent and prefer its enabled desktop, with Desktop and TMUX views of the same session. Opening Desktop raises the box's primary session window, which is titled with its tmux session name.
- **Grid**: multiple viewers for existing persistent interactive boxes, not a
  scheduler, launcher, or broadcast-input console.

Closing a viewer does not stop its box. Hibernating a persistent box stops its
processes and retains the volume; restoring a workspace is not proof that the old
process IDs survived. Deleting a box removes its workspace permanently. Do not
delete a shared fleet service when asked to delete a logical box.

## Code map

`Dockerfile` preinstalls Python/pip/venv/pipx, pinned uv/uvx, Node/npm/npx,
default-component Bun, and FFmpeg/ffprobe. Version probes are recorded in the
image manifest. Run
`bash tests/worker-tools.sh IMAGE` to verify these tools and offline virtualenv
creation as the workspace user in a disposable container. New image builds do
not upgrade existing workers or shared hosts; rollout is a separate operation.

The opt-in [shared-worker provider](SHARED-WORKERS.md) supports several logical
slots on one physical worker. Its supervisor is `internal/sharedworker`, provider
adapter is `internal/provider/shared`, and entry point is `cmd/vmbox-shared-worker`.
Each box selects an isolation tier (`uid` by default, `namespace` when configured
and verified) and reports it in connection metadata and box labels; isolation
status is reported, never assumed. Do not apply dedicated-worker whole-volume
cleanup to these boxes.

| Area | Entry points |
| --- | --- |
| Executables | `cmd/vbox`, `cmd/vmbox-controller`, `cmd/vmbox-runtime`, `cmd/vmbox-worker-agent` |
| CLI forms and tasks | `internal/cli/creation.go`, `controller_task.go`, `logical_boxes.go` |
| Routes and background reconciliation | `internal/controller/server.go` |
| Fleet creation, allocation, hibernation, deletion | `internal/controller/fleet_*.go` |
| Provider interfaces and transports | `internal/provider`, `internal/transport` |
| Persisted entities and migrations | `internal/controller/schema.sql` |
| Instruction presets, box snapshots, re-imported logins | `internal/controller/instruction_presets.go`, `login_profile_reimport.go`, `internal/boxruntime/instructions.go` |
| Chat/box image blob storage | `internal/controller/run_once_images.go` |
| Actual task execution and exit journal | `internal/boxruntime/process.go` |
| tmux identity, context and restoration | `internal/boxruntime/native.go`, `tmux_context.go`, `tmux.go` |
| Owner web terminal / desktop transport | `internal/controller/web_terminal.go` |
| Ordinary admin UI | `internal/controller/web/index.html`, `app.js`, `app.css` |
| Single-box UI | `internal/controller/web/workspace*` |
| Grid | `internal/controller/grid.go`, `web/grid.html`, `web/grid.js`, `web/grid.css` |
| Agent chats (WhatsApp-style, PWA, web push) | `web/chat.html`, `web/chat.js`, `web/chat.css`, `web/push-sw.js`, `internal/controller/web_push.go` |
| Imported-profile usage polling and cached owner view | `internal/controller/profile_usage.go`, `usage_probe.py`, `profile_usage_snapshots` in `schema.sql`, `GET /v1/profile-usage`, `POST /v1/profile-usage/refresh` |

Profile usage polling enumerates saved Claude/Codex/OpenCode profiles. It
prefers a running box with the imported profile; otherwise it checks the saved
credential in a private temporary home on the controller. Only credential
files are copied into that home, never uploaded settings or MCP configuration.
The controller polls about every 30 minutes; owners can request an immediate
refresh through the POST endpoint. The cached owner view never wakes a box and
identifies the result source.

Shared-worker resource usage comes from the worker's cgroup when it has a
finite memory limit (for example, Railway); otherwise it uses host memory
counters. The Providers page skips usage requests for pools with no active
worker. An older supervisor that lacks `host-resources` must be upgraded
before its usage can be shown.

Dedicated Railway workers can use an authenticated worker agent for terminal,
desktop, file and runtime traffic. Railway APIs and SSH remain infrastructure
and bootstrap paths. An enrolled worker that is offline does not fall back to
SSH for ordinary box traffic. The controller validates assignment, connection
epoch and tmux server incarnation on the direct path. Self-hosted shared workers
use their own supervisor transport. See [worker providers](PROVIDERS.md) for the
supported hosting paths.

## Grid implementation and invariants

Grid automatically fills tiles from the authorized interactive box inventory.
The default layout grows to display all running boxes; fixed layouts fill their
available tiles and replace lost boxes from the remaining inventory. Each tile
shows an enabled Desktop above a simultaneous TMUX connection; a running box
without an interactive session gets a persistent shell. One disconnected viewer
does not replace a box while its other viewer remains connected. Sleeping boxes
are never allocated or resumed.
Inventory refreshes every 15 seconds; failed connections cool down for 30 seconds.

Preserve account and one-shot exclusions in `GET /v1/grid-boxes`, per-tile version
fences for asynchronous requests, isolated input and clipboard controls, and
viewer-only disposal. Runtime assignment fences and stream authorization still
apply. Shared desktop/terminal helpers accept tile roots and disconnect callbacks.

## Other recently established behavior

- Live workspace streams (web terminal and VNC desktop) revalidate themselves
  periodically: the controller stream rechecks the assignment and browser
  session every 5 seconds, the direct-worker stream rechecks its binding every
  second, and the shared agent connection renews its lease every 20 seconds.
  A transient store failure inside these checks must never disconnect a healthy
  viewer by itself: streams tolerate such failures for a bounded grace window
  (`streamRevalidationGrace`) and then fail closed, while definitive evidence
  (assignment fence change, stopped box, revoked or expired token, replaced
  agent epoch, lost controller lease) still ends the stream or connection on
  the first observation. Regression tests live in
  `internal/controller/workspace_stream_revalidation_test.go`.

- Default worker builds include desktop packages (`VMBOX_DESKTOP=true`). Opening
  a sleeping web workspace is read-only until **Resume box** is clicked. A running
  workspace starts the desktop on attachment; TMUX retains its flow.
  PCManFM supplies desktop launch icons alongside the tint2 taskbar. Icon files
  are created only when absent; an exact legacy vbox Terminal icon is refreshed,
  while owner edits survive reconnects. libfm's quick-execute preference allows
  desktop launchers to open without an executable-file prompt; it also applies
  to other executable files opened through PCManFM. The desktop runtime polls
  `/data/workspace` every three seconds and mirrors its top-level real folders
  as symlinks in the configured Desktop directory. It records only links it
  created and removes stale links only if their targets remain unchanged, so
  owner files and custom launchers survive. New interactive shells use
  `VMBOX_DESKTOP_DISPLAY`: `:99` is the dedicated-worker fallback, while shared
  workers assign a distinct display to each workspace. Screenshot/input agent
  tools remain separate and should be used instead of assuming a display number.

- Blender's binary is available on PATH in fresh boxes from the common worker
  image. The optional preset configures Blender MCP and its add-on. New presets
  pin the official Linux x64 Blender 5.1.2 archive and its SHA-256 in
  `internal/boxruntime/blender_release.go`; the common worker image bundles the
  binary and MCP package under `/opt/vmbox`. The binary is linked into image PATH
  and into each tagged box's home.
  The image omits Blender's two static embedded-Python link archives; these are
  build-time artifacts, not needed to run Blender or its Python API. Verify a
  candidate image with `bash tests/worker-blender-runtime-image.sh IMAGE`; that
  smoke test runs `blender` as the box user in regular and login shells, then
  launches it headlessly.
  Older dedicated images retain the per-home verified download fallback; shared
  workers require the bundled image. Shared MCP listeners use the workspace UID
  as a stable loopback port, and both the add-on and client use that port.
  Existing legacy presets are not upgraded.
  `internal/boxruntime/blender.go` retains a `blender-enabled` marker under
  `~/.config/vmbox/`, separate from custom Bash: `1` retains the legacy distribution
  package, while `5.1.2` selects the pinned release. `RestoreToolSetup` restores the
  preset even without a custom script. The single-box workspace creates/reuses the selected managed session before
  presenting its preferred desktop view. Grid also prefers enabled desktops. The preset also installs pinned Blender MCP, enables its add-on,
  and registers its local stdio bridge for Codex and Claude (also OpenCode for new
  pinned presets) unless the user already
  has a `blender` MCP entry. Telemetry is disabled, bridge safe mode is enabled,
  and its Blender-side TCP listener stays on loopback. Both workspace viewers
  provide browser clipboard buttons.

- Agent model choices and extra arguments are literal argv entries, not shell
  text. Dropdown model IDs are common choices, not an account-entitlement API.
- Images can be selected, dropped, or pasted into Agent chat. Uploads use the same
  bounded image API, numbered references and appended download URLs. Normal text
  paste is not intercepted. URLs expire; never expose their access capability in logs.
- While an agent is processing, the chat shows a TV button beside the bubble:
  hovering first shows a worker-captured screenshot, then a view-only VNC
  stream. The preview stays open while the pointer enters it; clicking opens
  the Desktop/TMUX control popup. Its timeline replays bounded JPEG frames
  captured inside running boxes every 30 seconds and retained for 30 minutes
  in `desktop_replay_frames`; the owner-only endpoints never wake a stopped box.
- Agent contacts are controller-owned. `agent_roles`, `agent_role_permissions`,
  and `box_role_assignments` hold account-scoped editable tool bundles. A role
  may add the explicit `all_contacts` capability. Otherwise `box_contacts` is
  the box's directional direct-contact list. `box_protection` excludes a target
  from every contact list. The optional preset creates editable Manager and
  Normal roles; authorization still comes from their capabilities, never names.
  `GET /v1/agent-desktop/contacts` feeds the `get_contacts` tool; `chat_message`
  and `chat_ask` accept an optional `contact`, and the controller routes it into
  the target's existing native conversation (never a second session) with the
  sender recorded as `box_messages.sender_box_id` and direction `box`. Contact
  sends check the target's current state before queueing. Text sends also wait
  for the controller's delivery verdict and return a rejection to the caller;
  an uncertain controller response leaves the event in the durable outbox and
  is reported as queued and unconfirmed while automatic retries continue.
  Transient worker or database failures do not create permanent rejection
  bullets. Contact images still use the outbox fallback and report that
  confirmation is pending. Contact messages and direct agent replies to them
  appear in the separate owner-only
  Box ↔ Box transcript, with image attachments supported. The owner
  edits roles through `/v1/agent-roles` and `/v1/agent-role-assignments`, and
  direct contacts at `/v1/logical-boxes/{id}/contacts`, metadata labels at
  `/tags`, and protection at `/protection`. Both the controller and chat app
  expose editable roles, assignments, and creation-time selection; the workspace
  page and chat Details drawer expose each box's direct contacts and labels. Entry points:
  `internal/controller/agent_roles.go`, `internal/controller/contacts.go`,
  `internal/boxruntime/contacts.go`.
  The isolated worker supervisor uses `TasksMax=2048` so concurrent Docker
  session probes do not exhaust its systemd task limit.
- Agent-initiated `create_agent_box` inserts reciprocal direct-contact grants
  and contact events in the same transaction as the new logical box. An
  idempotent retry observes the existing grants. It also adds the child to
  the creator's Chat sidebar group; an ungrouped creator gets a new group
  containing both boxes. Sidebar groups are labels, not contact grants.
  `get_contacts` includes the caller's and each visible contact's cached
  profile usage when available. Remaining percentages use the lowest active
  usage window; stale or missing measurements are reported as such. The delegated
  `wake_agent_box` tool is available with the restart permission. It queues or
  allocates another hibernated, unprotected box without restarting a running
  one, and requires exact-name confirmation plus an idempotency key. The
  optional `sessionChoice` (`restore` or `fresh`) is saved on the allocation
  request and applied to the saved Codex, Claude, or OpenCode conversation
  after the box is running. Pending choices survive queued allocation and
  controller restart; the allocation reconciler retries them.
  `clear_agent_box_context` tool is available with the same permission and
  resets another running, unprotected box through the owner chat reset path;
  it requires an exact target name and idempotency key. The same permission
  grants `compact_agent_box_context`, which requests compaction in another
  running, idle, unprotected box's current conversation. Codex uses
  `thread/compact/start` on the visible thread; Claude and OpenCode submit their
  `/compact` command to the managed TUI. The tool reports request acceptance,
  not summary completion. The separately granted
  `get_agent_box_screenshot` tool returns an image of another running,
  unprotected box's current desktop. It uses the existing fenced worker
  capture path and never wakes the target or grants desktop control. The
  separately selected `set_agent_box_run_budget` admin tool requires the box
  management restart grant and exact target name confirmation. It changes the
  target's durable run-time limit and restarts its current countdown; zero
  disables that limit. Entry points:
  `internal/controller/agent_box_creation.go`,
  `internal/controller/fleet_create_store.go`,
  `internal/controller/agent_box_management.go`.
- The on-box `chat_message` and `chat_ask` MCP tools reject messages over 2,000
  Unicode characters before contact lookup or controller delivery. For
  `chat_ask`, the question and choices share that limit; contact questions also
  count the rendered choice labels.
- The chat PWA is mobile-first: a single-column app shell with push navigation
  on phones and a two-pane view from 900px. It ships a dark, Discord-like
  palette; a hex seed still shapes the seeded emoji mascot, the corner radii and
  the type face, and can be rerolled from the look sheet. Message bodies render
  through the injection-safe `markdown.js` (headings, lists, code, quotes, links)
  with bare URLs still linkified. Every attachment and media embed is a focusable
  control: images are buttons that open a focus-managed lightbox (video and audio
  included, focus restored on Escape) with left/right gallery stepping, and
  attached drafts can be inspected before sending. Attachments may be PNG/JPEG/GIF
  up to 25 MiB or MP4/WebM video up to 100 MiB (`/v1/run-once-images` sniffs the
  container); media is served with range requests so video can seek, and video is
  played from the authenticated same-origin endpoint rather than a blob. Unsent
  composer text is kept per box in local storage, and the transcript remembers its
  scroll position per box. The seeded mascot is used for box avatars (with a
  `NO SIGNAL` fallback) and the processing bubble.
- Persistent-box Agent chat links images to individual messages and displays them
  through an authenticated endpoint. Codex follow-ups use the visible thread's
  app-server queue with structured image inputs. After a fresh Codex TUI starts
  (including after hibernation or Clear), its first Chat text message enters
  that visible TUI. A first image message uses Codex's native startup image
  input in the same pane. This materializes the new thread before Chat uses
  app-server queueing: an idle thread may be absent from the recent-thread list
  while an older saved thread remains there. Claude uses its
  experimental `claude/channel`, or OpenCode's loopback session API. A Codex
  queue or steer acknowledgement is not a delivery receipt: Chat advances only
  after the exact user item appears in Codex's native history. Each pending
  message records the visible thread it targeted. If its turn ends without
  consuming the message, the receipt reconciler may queue its durable inbox
  copy once the same thread is visible and its native queue is empty. It must
  never replay an uncertain message into a different thread. Claude channel
  readiness is likewise not a delivery receipt; verify an exact native
  transcript entry and linked reply in a live box before changing its launcher
  flags. After changing chat transport code, run
  `VMBOX_TOKEN=... python3 scripts/chat-delivery-canary.py --url URL --box DISPOSABLE_BOX`
  against an idle, explicitly disposable box for each affected harness; a
  checkmark without the linked canary reply is not a pass. The managed
  `vmbox-desktop` MCP exposes `chat_message`, `chat_ask`, and `set_busy`.
  Submitted prompts mark the active task busy in the controller; replies and
  questions clear it. `set_busy` is the explicit override for activity outside
  that request/reply flow. Chat history returns the persisted state in response
  headers, so the UI does not guess that long-running work ended after ten
  minutes. Automatic clears are tied to the submitted message, so a late reply
  cannot hide a newer prompt that is still being processed.
  The box-local harness MCP process checks its managed conversation every 10
  seconds. It reads the active native Codex, Claude, or OpenCode transcript,
  caps the recent text sample at 8 KiB, and posts it when the text changes
  through the assignment-scoped DesktopAgent route. The controller applies a
  compact trained mood classifier and a local phrase generator. It stores
  derived state, a digest of the sampled text, and observation/change times on
  the active task, never the transcript sample. The phrase generator keeps its
  calibrated confidence gate; an explicit next action in the newest agent line
  can supply a short transcript-derived phrase. A busy box
  without a reliable phrase shows Working; repeated unchanged samples for 90
  seconds show Idle. The keepalive updates the heartbeat time, while a missing
  heartbeat for 40 seconds makes activity Unknown. A new busy transition resets
  the quiet window. Chat history returns fresh state in response headers.
  The MCP sender reads native conversation text and does not capture tmux output.
  Entry points:
  `internal/boxruntime/mascot_observation.go`, `mascot_transcript.go`,
  `internal/controller/mascot_classifier.go`, `web/chat.js`. Training and
  evaluation are described in [the mascot classifier guide](MASCOT-CLASSIFIER.md).
  `chat_message` writes a message on its own; passing `replyTo` (the short chat
  key carried in the envelope) answers one specific message. The box durably
  queues each MCP event and pushes text events through its scoped chat-ready
  callback. The controller confirms storage before the box removes its outbox
  copy. Image events, older runtimes, and failed callbacks use the existing
  worker outbox pull/ack path; chat reads, reconciliation, and reply watchers
  continue to drain that fallback.
  A late or repeated reply whose message is already answered is stored as its own
  agent message, so the outbox can never head-of-line block. Terminal capture
  remains a compatibility fallback for clients that do not call the tool. The
  same MCP exposes the desktop tools (`take_screenshot`, `click_mouse`,
  `type_text`, `press_keys`), so an agent can operate the box's computer.
  Local scripts can send text into a running managed conversation with the
  authenticated box-side `POST /prompt` endpoint. It does not wake an agent;
  the [local prompt API guide](LOCAL-AGENT-PROMPT.md) covers its contract and
  retry limits. The generated `~/.config/vmbox/mcp-tools.md` includes the
  request format for agents working inside the box.
- The optional Lifecycle MCP tool `heartbeat` manages a single box-local timer
  in `~/.local/share/vmbox/heartbeat.json`. Call it with
  `{"action":"start","intervalMinutes":5,"count":2}` to schedule ticks, or
  `{"action":"stop"}` to cancel them.
  The box's local MCP HTTP façade watches the private state file and sends
  due prompts through its own `/prompt` endpoint with a stable message ID.
  Ticks are at least five minutes apart, count defaults to one, and multi-tick
  prompts include the remaining count. The timer resumes from the volume
  after a box wake and never wakes a hibernated box. The controller supplies
  the ordinary MCP tool policy, but does not schedule or store heartbeat ticks.
- Each box-side desktop MCP tool invocation queues a small local activity record.
  The box-local HTTP facade sends it to the controller, which shows a system
  bullet in that box's Chat. A stable event ID makes retries idempotent and the
  queue survives a box wake. Only the tool name, heartbeat action, whether a
  contact was targeted, and success/failure are stored; arguments, message
  bodies, paths, credentials, and screenshots are excluded. Direct replies to
  the box's own Chat already appear there and get no bullet.
- A new OpenCode Agent chat starts a bare TUI, waits for the visible bridge, then
  submits its first message through the loopback API with structured image parts.
  Persistent OpenCode and OpenCode one-shot
  tasks start with `--auto`; explicit client deny rules still take precedence.
- Foundry is a pinned preset. Custom tooling is trusted user-supplied Bash run
  **inside the worker**, with a five-minute deadline, before the task. Persistent
  boxes retain and rerun the recipe on resume: installation must be idempotent.
- The owner controller UI loads current-period fleet service costs only when the
  owner asks. Cost reads do not start or contact boxes. Railway batches all slot
  lookups into one billing command; credentials without billing scope return an
  unavailable explanation instead of a fabricated estimate.
- `process_tasks` cascade when a box is deleted. Never infer task success from SSH errors.
- Login profiles can be created in Manage → Profiles with a bounded, owner-scoped
  browser login session for the official Codex and Claude CLIs or a verified
  provider API key. The CLI upload path remains available. Browser login CLIs
  run with a private temporary home and scrubbed environment; the terminal is
  attached to that fixed login command, not a general shell. Codex offers a
  ChatGPT device code or normal browser OAuth link. For browser OAuth, the owner
  pastes the browser's failed `127.0.0.1:1455/auth/callback` URL into the
  dialog; the controller checks the active session's state and forwards only to
  the local Codex callback listener. Browser OAuth sessions are serialized
  because the CLI uses a fixed callback port. Saved credentials
  use the same encrypted account profile store. A box has at most one imported agent
  profile, and its application authoritatively selects the managed harness.
  Replacing it uses the locked, integrity-checked transfer, removes portable
  credential/config files for other harnesses, fails stale chat tasks, kills only
  managed agent tmux sessions, and rewrites the tmux snapshot so an old harness
  cannot return after deployment. Shell/desktop sessions survive. Stopped boxes
  queue the same reconciliation for their next allocation. Managed agent restore
  always uses `agent-restore`, which reconstructs current channel/API/backend
  arguments instead of launching a bare executable. Deleting a controller-saved
  profile does not revoke a copy already imported into a box.
- Instruction presets are account-scoped Markdown snapshotted per box
  (`box_instruction_snapshots`), never referenced live: preset edits/deletions
  cannot change existing boxes. `internal/boxruntime/instructions.go` keeps one
  canonical `~/.config/vmbox/instructions.md` and links it into exactly the
  verified global slots (`~/.codex/AGENTS.md`, `~/.claude/CLAUDE.md`,
  `~/.config/opencode/AGENTS.md`), tracking ownership in a ledger. Pre-existing
  user files are conflicts left untouched, repository instruction files are
  never written, and re-application on every attach (`sync-instructions` before
  `restore-tools`, plus `RestoreManagedInstructions` inside restore) is
  idempotent. `vmbox-runtime sync-instructions` verifies its payload digest like
  `sync-files`. New creations store compact selected-tool path references in a
  separate `tool_guidance` column and compose them with the user snapshot only
  when syncing to the box; editing the snapshot preserves the references.
  Existing rows default to empty guidance and are not backfilled. The generated
  section is bounded by the runtime's 64 KiB instruction limit before creation.
  The controller and Chat UIs offer an optional concise-response template; it
  becomes editable custom Markdown only for the box where it is selected.

## Verification and honest evidence

```sh
go test ./...
go vet ./...
npm ci --ignore-scripts
npm run test:browser
git diff --check
```

Go 1.26 is required. PostgreSQL integration tests require
`VMBOX_TEST_DATABASE_URL` pointing to a **disposable** database, never production.
They skip without it; a green skipped run is not database proof. In particular,
`TestGridExcludesBusyAndOtherAccounts` checks account boundaries and excludes
boxes currently running a one-shot process.

`tests/browser/controller-ui.test.mjs` covers admin UI behavior with fixture APIs.
`tests/browser/grid.test.mjs` uses real isolated local tmux shells connected to
Chromium through WebSockets. It executes unique shell calculations, compares
independent tmux captures, and verifies displayed output, four-tile viewport fit,
input isolation, fullscreen, reconnect/process survival, layout changes, stale
response rejection, mobile stacking, and absence of lifecycle API writes.

The grid test requires native `tmux`, `script`, and Chromium (default
`/snap/bin/chromium`, override `VMBOX_CHROMIUM`). Its metadata/API and SSH bridge
are local test plumbing, not a real-provider end-to-end test. Desktop
emulation is not proof of a real mobile keyboard or clipboard. Keep these limits
explicit when reporting results. A desktop screenshot is written to
`/tmp/vmbox-grid-desktop.png` for visual inspection.

For a production smoke check, verify the deployed revision, authenticate using
secure environment configuration, check `/grid` and `/v1/grid-boxes`, and compare
eligibility with actual account state. Do not create/resume worker boxes just to
populate a screenshot without authorization. Opening a grid must itself make no
allocation, session-creation, hibernation or deletion requests.

## Working safely in this repository

Inspect `git status` before edits. There may be unrelated, uncommitted interaction
drafts; preserve them and keep release commits scoped. Do not treat local drafts
as deployed code. Older factory/coworker design documents and dated verification
reports describe previous iterations; use current routes, source and schema as
authority. Do not reintroduce an orchestrator merely because an old plan mentions it.
