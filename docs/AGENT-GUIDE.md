# vmbox: guide for future agents

Current architecture and behavior, checked against the source on 2026-09-10.
Start with the [setup guide](../README.md). This document explains where to work
and the distinctions that must survive future changes.

## What the product is

The controller owns provider configuration, credentials, logical boxes, persistent
storage, and a reusable fleet of compute slots. The CLI is controller-first and
provider-agnostic. A logical box is **not** a fleet service: its workspace volume
can outlive, and later attach to, a different compute slot.

- `vmbox BOX`: persistent interactive shell/tmux workspace; launch agents yourself.
- Web box workspace: attach to that box's tmux terminal, with optional desktop.
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

Provider APIs manage infrastructure. The terminal carries real terminal bytes over
WebSocket → controller → provider SSH stream → fenced native tmux attachment.
The controller resolves the current deployment; don't reuse stale instance IDs.
Session identity includes the assignment and tmux server incarnation, not just a
reusable session name. Preserve those checks when changing transport code.

## Grid implementation and invariants

Open `/grid` from the controller's **Grid** link. Layout defaults to 2 columns ×
2 rows; controls support 1–4 columns and 1–2 rows. On desktop the tiles share the
available viewport height. At 700px or narrower they stack and scroll vertically.

1. `GET /v1/grid-boxes` is owner-authorized. It starts from the account-scoped box
   list, excludes all boxes recorded in `run_once_requests`, and excludes boxes
   with unfinished `process_tasks`. It does not use name prefixes or the paginated
   Run once history to decide box eligibility. No provisioning occurs here.
2. Choosing a running box reads `/sessions` and `/sessions/primary`. It selects
   the remembered primary when still live, otherwise an existing interactive
   session. Incomplete inventories fail visibly. `task-*` sessions are not offered.
3. Each tile calls `openWorkspaceTerminal(boxID, session, onStatus, options)`.
   `options.root` and `options.keys` scope its DOM; `autoFocus:false` prevents a
   later connection stealing focus from the user's active tile. The original
   single-workspace caller retains its defaults.
4. Every tile owns its xterm instance, WebSocket, resize observer and disposer.
   Focus determines keyboard input; there is no broadcast handler. Resize frames
   go to that tile's connection. Existing sessions may also have other attached
   clients; normal tmux multi-client sizing behavior still applies.
5. Per-tile version counters reject delayed session lookups and status callbacks
   after selection changes. Disposal closes only the viewer and suppresses late
   output/status events. Clear, layout reduction and logout dispose affected
   connections without stopping remote shells.
6. The box list refreshes every 15 seconds. Removed, non-interactive or no-longer-
   running boxes are disconnected. Reconnect is explicit: there is no automatic
   allocation, wake-up, new session, replay of typed input, or persisted selection.

The existing backend stream limits also apply: 32 simultaneous web workspace
streams per controller process, shared with desktop, and an eight-hour stream
timeout. Grid layouts are bounded to eight tiles. This is not a new transport or
a bypass for stream authorization, origin checks, assignment fences, or deadlines.

## Other recently established behavior

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
