# vmbox: guide for future agents

Current architecture and behavior, checked against the source on 2026-09-10.
Start with the [setup guide](../README.md). The [desktop MVP guide](AGENT-DESKTOP-IMPLEMENTATION.md) records the newer desktop/secret/idle implementation and verification. This document explains where to work
and the distinctions that must survive future changes.

## What the product is

The controller owns provider configuration, credentials, logical boxes, persistent
storage, and a reusable fleet of compute slots. The CLI is controller-first and
provider-agnostic. A logical box is **not** a fleet service: its workspace volume
can outlive, and later attach to, a different compute slot.

- `vmbox BOX`: persistent interactive shell/tmux workspace; launch agents yourself.
- Web box workspace: start the selected managed agent and prefer its enabled desktop, with Desktop and TMUX views of the same session.
- **Run once**: disposable agent/shell run, not a multi-agent task orchestrator.
  Actual process exit records completion. An idle completed one-shot box and its
  volume are deleted; saved output and exit code remain in the controller.
- **Grid**: multiple viewers for existing persistent interactive boxes, not a
  scheduler, launcher, broadcast-input console, or one-shot results viewer.

Closing a viewer does not stop its box. Hibernating a persistent box stops its
processes and retains the volume; restoring a workspace is not proof that the old
process IDs survived. Deleting a box removes its workspace permanently. Do not
delete a shared fleet service when asked to delete a logical box.

## Code map

| Area | Entry points |
| --- | --- |
| Executables | `cmd/vmbox`, `cmd/vmbox-controller`, `cmd/vmbox-runtime` |
| CLI forms and tasks | `internal/cli/creation.go`, `controller_task.go`, `logical_boxes.go` |
| Routes and background reconciliation | `internal/controller/server.go` |
| Fleet creation, allocation, hibernation, deletion | `internal/controller/fleet_*.go` |
| Provider interfaces and transports | `internal/provider`, `internal/transport` |
| Persisted entities and migrations | `internal/controller/schema.sql` |
| Run once queue, archived results, images | `internal/controller/run_once*.go` |
| Actual task execution and exit journal | `internal/boxruntime/process.go` |
| tmux identity, context and restoration | `internal/boxruntime/native.go`, `tmux_context.go`, `tmux.go` |
| Owner web terminal / desktop transport | `internal/controller/web_terminal.go` |
| Ordinary admin UI | `internal/controller/web/index.html`, `app.js`, `app.css` |
| Single-box UI | `internal/controller/web/workspace*` |
| Grid | `internal/controller/grid.go`, `web/grid.html`, `web/grid.js`, `web/grid.css` |

The selected Railway-only plan is [Direct connections to Railway workers](RAILWAY-DIRECT-WORKERS.md):
keep worker hosting on Railway and use authenticated worker agents for running-box
connections. It requires no measurement phase and preserves existing worker
processes during migration. [Railway independence](RAILWAY-INDEPENDENCE.md) is a
separate external-host proposal, not a dependency of the selected plan.

Provider APIs manage infrastructure. The current production terminal carries real
terminal bytes over WebSocket → controller → Railway SSH stream → fenced native
tmux attachment. The controller resolves the current deployment; don't reuse stale
instance IDs. Session identity includes the assignment and tmux server incarnation,
not just a reusable session name. Preserve those checks while making the transport
provider-neutral.

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

- Default worker builds include desktop packages (`VMBOX_DESKTOP=true`). The web
  workspace starts the desktop on attachment; TMUX and Run once retain their flows.
  PCManFM supplies desktop launch icons alongside the tint2 taskbar. Icon files
  are created only when absent; owner edits survive reconnects. New interactive
  shells default `DISPLAY` to `:99`; screenshot/input agent tools remain separate.

- Blender is an optional distribution-package preset, including desktop packages.
  `internal/boxruntime/blender.go` retains a `blender-enabled` marker under
  `~/.config/vmbox/`, separate from custom Bash. `RestoreToolSetup` restores the
  preset even without a custom script. The single-box workspace creates/reuses the selected managed session before
  presenting its preferred desktop view. Run once retains its terminal flow; Grid also prefers enabled desktops. The preset also installs pinned Blender MCP, enables its add-on,
  and registers its local stdio bridge for Codex and Claude unless the user already
  has a `blender` MCP entry. Telemetry is disabled, bridge safe mode is enabled,
  and its Blender-side TCP listener stays on loopback. Both workspace viewers
  provide browser clipboard buttons.

- Run once supports documented Codex model IDs and Claude aliases in a dropdown,
  plus saved-profile defaults and custom IDs. These are common choices, not an
  account-entitlement API. Extra arguments are literal argv entries, not shell text.
- Images can be selected, dropped, or pasted. Uploads use the same bounded image
  API, numbered references and appended download URLs. Normal text paste is not
  intercepted. URLs expire; never expose their access capability in logs.
- Foundry is a pinned preset. Custom tooling is trusted user-supplied Bash run
  **inside the worker**, with a five-minute deadline, before the task. Persistent
  boxes retain and rerun the recipe on resume: installation must be idempotent.
- `process_tasks` cascade when a box is deleted. Run once must first archive the
  completed result into `run_once_requests.result`. The results page must work
  when the logical box no longer exists. Never infer task success from SSH errors.
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
`TestGridExcludesOneShotAndOtherAccounts` checks account boundaries and excludes
both disposable boxes and persistent boxes currently running a one-shot process.

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
