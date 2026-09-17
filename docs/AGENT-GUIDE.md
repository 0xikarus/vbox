# vmbox: guide for future agents

Current architecture and behavior, checked against the source on 2026-09-14.
Start with the [setup guide](../README.md). The [desktop MVP guide](AGENT-DESKTOP-IMPLEMENTATION.md) records the newer desktop/secret/idle implementation and verification. This document explains where to work
and the distinctions that must survive future changes.

## What the product is

The controller owns provider configuration, credentials, logical boxes, persistent
storage, and a reusable fleet of compute slots. The CLI is controller-first and
provider-agnostic. A logical box is **not** a fleet service: its workspace volume
can outlive, and later attach to, a different compute slot.

- `vmbox BOX`: persistent interactive shell/tmux workspace; launch agents yourself.
- Web box workspace: start the selected managed agent and prefer its enabled desktop, with Desktop and TMUX views of the same session.
- **Grid**: multiple viewers for existing persistent interactive boxes, not a
  scheduler, launcher, or broadcast-input console.

Closing a viewer does not stop its box. Hibernating a persistent box stops its
processes and retains the volume; restoring a workspace is not proof that the old
process IDs survived. Deleting a box removes its workspace permanently. Do not
delete a shared fleet service when asked to delete a logical box.

## Code map

`Dockerfile` preinstalls Python/pip/venv/pipx, pinned uv/uvx, Node/npm/npx and
default-component Bun. Version probes are recorded in the image manifest. Run
`bash tests/worker-tools.sh IMAGE` to verify these tools and offline virtualenv
creation as the workspace user in a disposable container. New image builds do
not upgrade existing workers or shared hosts; rollout is a separate operation.

The opt-in [shared-worker provider](SHARED-WORKERS.md) supports several logical
slots on one physical worker. Its supervisor is `internal/sharedworker`, provider
adapter is `internal/provider/shared`, and entry point is `cmd/vmbox-shared-worker`.
Do not apply dedicated-worker whole-volume cleanup to these boxes.

| Area | Entry points |
| --- | --- |
| Executables | `cmd/vmbox`, `cmd/vmbox-controller`, `cmd/vmbox-runtime`, `cmd/vmbox-worker-agent` |
| CLI forms and tasks | `internal/cli/creation.go`, `controller_task.go`, `logical_boxes.go` |
| Routes and background reconciliation | `internal/controller/server.go` |
| Fleet creation, allocation, hibernation, deletion | `internal/controller/fleet_*.go` |
| Provider interfaces and transports | `internal/provider`, `internal/transport` |
| Persisted entities and migrations | `internal/controller/schema.sql` |
| Chat/box image blob storage | `internal/controller/run_once_images.go` |
| Actual task execution and exit journal | `internal/boxruntime/process.go` |
| tmux identity, context and restoration | `internal/boxruntime/native.go`, `tmux_context.go`, `tmux.go` |
| Owner web terminal / desktop transport | `internal/controller/web_terminal.go` |
| Ordinary admin UI | `internal/controller/web/index.html`, `app.js`, `app.css` |
| Single-box UI | `internal/controller/web/workspace*` |
| Grid | `internal/controller/grid.go`, `web/grid.html`, `web/grid.js`, `web/grid.css` |
| Agent chats (WhatsApp-style, PWA, web push) | `web/chat.html`, `web/chat.js`, `web/chat.css`, `web/push-sw.js`, `internal/controller/web_push.go` |

The [direct-worker rollout](RAILWAY-DIRECT-WORKERS.md) keeps worker hosting on
Railway. Each enrolled box has an authenticated worker agent; the controller sends
terminal, desktop, file and runtime traffic through that agent. Railway APIs and
SSH remain infrastructure/bootstrap paths, including initial installation and
compute replacement. An enrolled worker that is offline does not fall back to
Railway SSH for ordinary box traffic. The controller validates assignment,
connection epoch and tmux server incarnation on the direct path. The
[acceptance audit](DIRECT-WORKER-ACCEPTANCE.md) separates local coverage from
unfinished live verification. [Railway independence](RAILWAY-INDEPENDENCE.md) is
a separate external-host proposal, not a dependency of this rollout.

## Grid implementation and invariants

Grid automatically fills tiles from the authorized interactive box inventory.
The default layout grows to display all running boxes; fixed layouts fill their
available tiles and replace lost boxes from the remaining inventory. Each tile
prefers enabled Desktop and offers TMUX; a running box without an interactive
session gets a persistent shell. Sleeping boxes are never allocated or resumed.
Inventory refreshes every 15 seconds; failed connections cool down for 30 seconds.

Preserve account and one-shot exclusions in `GET /v1/grid-boxes`, per-tile version
fences for asynchronous requests, isolated input and clipboard controls, and
viewer-only disposal. Runtime assignment fences and stream authorization still
apply. Shared desktop/terminal helpers accept tile roots and disconnect callbacks.

## Other recently established behavior

- Default worker builds include desktop packages (`VMBOX_DESKTOP=true`). Opening
  a sleeping web workspace is read-only until **Resume box** is clicked. A running
  workspace starts the desktop on attachment; TMUX retains its flow.
  PCManFM supplies desktop launch icons alongside the tint2 taskbar. Icon files
  are created only when absent; an exact legacy vmbox Terminal icon is refreshed,
  while owner edits survive reconnects. libfm's quick-execute preference allows
  desktop launchers to open without an executable-file prompt; it also applies
  to other executable files opened through PCManFM. New interactive
  shells default `DISPLAY` to `:99`; screenshot/input agent tools remain separate.

- Blender is an optional preset, including desktop packages. New presets pin the
  official Linux x64 Blender 5.1.2 archive and its SHA-256 in
  `internal/boxruntime/blender_release.go`; the common worker image bundles the
  binary and MCP package under `/opt/vmbox`, linked into each tagged box's home.
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
  hovering previews the box desktop, and clicking opens the control popup on the
  Desktop tab.
- Persistent-box Agent chat links images to individual messages and displays them
  through an authenticated endpoint. Follow-ups use `codex queue`, Claude's
  experimental `claude/channel`, or OpenCode's loopback session API. The managed
  `vmbox-desktop` MCP exposes `chat_message` and `chat_ask`.
  `chat_message` writes a message on its own; passing `replyTo` (the short chat
  key carried in the envelope) answers one specific message. The controller polls
  each active task's outbox while a chat window is open, from the reconciler, and
  while a reply is awaited, and acknowledges an event only after it is stored.
  A late or repeated reply whose message is already answered is stored as its own
  agent message, so the outbox can never head-of-line block. Terminal capture
  remains a compatibility fallback for clients that do not call the tool. The
  same MCP exposes the desktop tools (`desktop_screenshot`, `desktop_click`,
  `desktop_type`, `desktop_key`), so an agent can operate the box's computer.
- A new OpenCode Agent chat passes its first message with native `--prompt`, then
  uses the loopback API for follow-ups. Persistent OpenCode and OpenCode one-shot
  tasks start with `--auto`; explicit client deny rules still take precedence.
- Foundry is a pinned preset. Custom tooling is trusted user-supplied Bash run
  **inside the worker**, with a five-minute deadline, before the task. Persistent
  boxes retain and rerun the recipe on resume: installation must be idempotent.
- The owner controller UI loads current-period fleet service costs only when the
  owner asks. Cost reads do not start or contact boxes. Railway batches all slot
  lookups into one billing command; credentials without billing scope return an
  unavailable explanation instead of a fabricated estimate.
- `process_tasks` cascade when a box is deleted. Never infer task success from SSH errors.
- Login profiles are uploaded from the CLI; selecting a profile copies credentials
  to a box. Deleting a saved profile does not revoke copies already on workers.

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
are local test plumbing, not a four-worker Railway end-to-end test. Desktop
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
