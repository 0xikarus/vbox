# Controller-first CLI

The controller owns providers, defaults, allocation, lifecycle and task state.
The CLI uses HTTP plus native OpenSSH; it has no provider adapters, token lookup,
provider CLI dependency or standalone fallback. The web application is configuration
only: no terminal, task launch, message, forwarding, or group polling. Historical
records and backend integration/message APIs are retained, not deleted.

## Setup and migration

```
vmbox context add team --controller https://YOUR-CONTROLLER --token-env VMBOX_CONTROLLER_TOKEN
vmbox providers schema
vmbox providers create railway primary --config-file railway.json --secret-env PROVIDER_SECRET_JSON
vmbox providers default railway primary
vmbox ls --json
vmbox sessions BOX --json
vmbox BOX --session NAME
```

Supply the controller token through secure environment configuration. Secret input
is a JSON object in the named environment variable; never a raw command-line token.
For Railway, config includes `projectId`, `environmentId`, `tokenEnvironment`
(`RAILWAY_TOKEN` for a project token, `RAILWAY_API_TOKEN` for an account token),
and optional `image`. Secret JSON contains `token`. Do not put secrets in config.

New contexts contain only controller URL and token reference. Existing controller
contexts are readable without rewriting them. Before creating/scaling resources,
their legacy provider selection must match the controller default; mismatch fails.
To migrate, explicitly select the matching alias on the controller, create a new
controller-only context under a different name, and switch to it. Existing contexts
are never overwritten by `context add`. Defaults cannot be silently retargeted.
Provider-only contexts and `--standalone` fail without touching existing resources.
The installer installs only the CLI and preserves legacy deployment bundles.

An old controller lacks `/v1/capabilities`: new attachment fails before allocation.
Old clients can retain read/lifecycle APIs, but credential PUT is now create-only;
existing-alias updates must use PATCH with a revision. Web terminal APIs remain for
compatibility/integrations, but are no longer used by the web application.

### Operator bootstrap

Provider-specific bootstrap is excluded from normal builds. Operators can build
`go build -tags vmbox_operator -o vmbox-bootstrap ./cmd/vmbox-bootstrap` and run
`vmbox-bootstrap init|ensure` with the previously supported bootstrap flags.
It reads an explicitly prepared legacy Railway context containing project,
environment, image and controller settings. It requires separate provider authority.
It is not installed with `install.sh` or invoked as an automatic fallback.

## Sessions and transport

Native attachment requires the **account owner** role. Being the owner of a logical
box under a normal user account does not grant SSH access. Users retain scoped
box/task/update access; removing their web terminal does not expand permissions.

Controller login does not register an SSH key. Provide a provider-registered SSH
identity via the SSH agent or `VMBOX_SSH_IDENTITY_FILE`. Optionally select an existing
trusted `VMBOX_SSH_KNOWN_HOSTS_FILE`. New hosts use TOFU (`accept-new`); changed keys
fail closed. Verify rotation independently before an operator changes known hosts.
The client disables SSH config injection, forwarding, shared masters and retries.
Only validated `user@host` OpenSSH endpoints are supported; other transports fail.
No provider secrets or arbitrary local executable commands come from the handoff.

New allocations bind a tmux-server assignment fence after restoration. Existing
running boxes require an explicit owner migration after controller deployment:
`vmbox sessions BOX --enable`. This stages the matching runtime and binds the
existing server while holding the assignment lock; it does not restart workers,
kill sessions or create a session. An unavailable runtime/fence fails closed.

`vmbox BOX` requires a terminal before allocation. It offers a Codex/Claude/OpenCode/shell
picker (defaulting to the configured box agent) and existing session names for
reconnection. `vmbox BOX claude` or `vmbox BOX shell` bypasses the picker and creates
a new interactive session. `--session NAME` selects an exact existing name, never
creates/replaces it. Detached interactive sessions keep the box running.
Runtime IDs and assignment-bound incarnations prevent prefix matches and attaching
to a same-name replacement. A random server incarnation also prevents ID reuse
after tmux restart/re-enabling. The fence check and attach share a tmux command queue.
Manual names with spaces/Unicode are preserved; names are not shell-interpolated.
Reads (`ls`, `status`, `sessions`, `updates`) never allocate or wake compute.

`vmbox task BOX AGENT --prompt TEXT --idempotency-key KEY --json` launches one-shot
Codex (`exec`), Claude (`-p`), OpenCode (`run --auto`), or shell (`bash -lc`)
execution. `--agent` remains a compatibility alias. No interactive
screen parsing is used. `task-status BOX [TASK_ID]` reports execution state and
nullable exit code; `task-output BOX TASK_ID` returns cached output JSON. An agent
tracks the meaning/progress of its own prompt; vmbox does not assess that work.

New `/process-tasks` APIs are separate from legacy interactive `/tasks` APIs; old
interactive histories and sessions are not converted. Status can still retrieve
an old interactive task by explicit ID. One-shot listings contain one-shot tasks
only. Old clients submitting `/tasks` keep their old interactive behavior.

The worker keeps a durable claim and result journal in `/data/.vmbox/processes/`.
Retries never execute an already claimed process again. Combined stdout/stderr
is drained and the first 1 MiB retained, with truncation explicitly indicated.
Results are copied to controller storage before automatic hibernation and remain
readable while compute is off. States are queued/starting/running/unknown/exited/
launch_failed. `exited` with code 0 means process success only; nonzero is the real
process failure code. Signal termination has a separate signal and null exit
code. Unknown outcomes never imply completion or permit auto-hibernation.

After completion (including failure), the controller hibernates only when no
pending/unknown task, legacy active task, or live tmux session remains. Submission,
managed interactive creation, and the idle decision serialize on the box row and
assignment fence. Interactive sessions are persistent even after client detach.

## Updates

`vmbox updates [BOX] [--json]` probes on demand and retains only names, identities,
fingerprints, observation state and revisions, not transcripts. Limits: 32 boxes
per CLI call, 64 sessions and 128 panes per box, 200 history lines/1 MiB per pane, 256 unread
records returned. Coverage caps are explicitly marked partial. Snapshots may miss
transient output; changed output is not agent completion or a needs-input event.
First observation is `baseline`, later changes `changed`, verified absence `exited`.
Offline/SSH failure is not session completion. Hibernation preserves workspace,
not live-process identity; restored sessions have new incarnations.

Reads and attachment do not acknowledge. Use
`vmbox updates ack BOX --session NAME --revision REV` for the exact displayed
revision. A stale, foreign or unknown revision is rejected (409), never advances
past concurrent output. Checkpoints are per account/user/box/incarnation and shared
across clients. Late probes cannot overwrite newer observations. Observation and
ack metadata survive controller restarts; agent lifecycle notifications are deferred.

## Provider administration

`providers list|show|schema|create|update|validate|default` use the same encrypted
credential store as the web. Output is JSON; diagnostics go to stderr. Updates
accept `--config-file FILE` (`-` means stdin), `--secret-env NAME`, and optional
`--revision UPDATED_AT`. Without a supplied revision the CLI fetches one first.
PATCH requires `If-Match` equal to `updatedAt`. Omitted secrets are preserved;
replacement is explicit. `null` deletes the editable `image` config field.

Conservative migration policy: target/config fields other than `image` are immutable
even when currently unused; create a new alias for retargeting. Provider deletion
and changing an established default require a separately reviewed resource migration.
This intentionally avoids races with concurrent allocation. Config changes do not
deploy/restart/reassign anything. Resolvers read fresh vault state for future calls;
already-running operations may finish with the credentials they resolved earlier.
`validate` only probes read inventory and explicitly lists untested permissions.
Legacy credential PUT cannot bypass revision checks by rotating existing aliases.

`status BOX --json` returns box state, tasks, and timestamped cached observations;
it explicitly marks them cached/unknown. Session inventory includes historical task
metadata matched by name, not a claim that a recreated session runs that old task.
CLI API error exit categories: 2 request error, 3 authorization, 4 conflict,
5 unavailable/rate-limited; local errors use 1. No diagnostics pollute JSON stdout.

## Rollout and evidence

Do not deploy solely because unit tests pass. Record deployed revision and existing
worker process/session identities. Deploy additive database/controller changes only
after approval, then explicitly enable native sessions on disposable/test boxes.
The existing user-owned Codex/reconciliation drafts remain uncommitted and undeployed.
No agent/model/authentication substitution is permitted to turn a live failure green.

Rollback uses the previous controller image, retaining all new metadata tables.
Do not reverse/drop schema, delete volumes or kill existing tmux sessions. The
operator must restore the matching runtime deliberately if needed, not restart
workers as a proxy for controller recovery. The old seven-case web-terminal audit
was not completed and is not claimed as a pass; its terminal tests are retired.

Verification: full Go tests/vet; isolated real tmux/PTY byte recording; disposable
PostgreSQL integration; desktop and 390×844 configuration browser tests; installer
checks. Real provider SSH/restart/recovery and live Claude/Codex/OpenCode prompts
remain deployment gates where local testing cannot establish production behavior.
